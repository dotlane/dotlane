# End-to-end test on a real Windows PC: install -> add -> HTTPS through the system's DNS and trust ->
# up/down -> doctor -> upgrade -> uninstall --purge, then check nothing is left. It changes the system
# (a scheduled task, the machine's Trusted Root store, a DNS rule), so it runs on GitHub's
# throwaway windows-latest runners, which are already administrators (no UAC prompt: dotlane
# sees the elevated token and runs its admin steps directly). Not for a development PC: it uses the
# default ports (53, 80, 443) and would collide with a real install.
#
# Runs in PowerShell 7 (pwsh) and in Windows PowerShell 5.1 (powershell.exe).
#   pwsh -NoProfile -File scripts/integration-windows.ps1
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
# The Cert: drive used below comes with this module; load it up front, since Windows PowerShell 5.1
# only creates the drive once the module is in.
Import-Module Microsoft.PowerShell.Security

$TLD = 'dltest'
$SERVICE = 'dev.dotlane.ci'
$WORK = Join-Path ([IO.Path]::GetTempPath()) ('dotlane-it-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
$CONFIG = Join-Path $WORK 'config.toml'
$STATE = Join-Path $WORK 'state'
$WWW = Join-Path $WORK 'www'
$BIN = Join-Path (Get-Location).Path 'bin\dotlane.exe'
# Where the service keeps its copy of the binary, and its log (internal/install/windows.go).
$SERVICE_EXE = Join-Path $env:LOCALAPPDATA "Programs\dotlane\$SERVICE.exe"
$LOG = Join-Path $env:LOCALAPPDATA "dotlane\logs\$SERVICE.log"
$TASK_PATH = '\Dotlane\'
$RULE_COMMENT = "dotlane:$SERVICE"
$script:Upstream = $null
$script:DaemonBefore = ''
$script:DaemonAfter = ''

function Step($message) { Write-Host "`n== $message" }
function Fail($message) { throw $message }

# ConvertTo-Arg quotes one argument for a Windows command line (CommandLineToArgvW rules).
function ConvertTo-Arg([string]$Arg) {
    if ($Arg -eq '') { return '""' }
    if ($Arg -notmatch '[\s"]') { return $Arg }
    return '"' + ($Arg -replace '(\\*)"', '$1$1\"' -replace '(\\+)$', '$1$1') + '"'
}

# Invoke-Dotlane runs the binary with a time limit and returns its exit code and output. Every
# dotlane call gets two minutes: a step waiting on a prompt nobody can answer (a UAC prompt,
# say) fails here, named, instead of hanging the job. Output is read as UTF-8 (dotlane
# prints non-ASCII marks), stdout and stderr kept apart, since stderr is where an upgrade reports
# itself.
function Invoke-Dotlane([string[]]$Arguments, [string]$Dir = $WORK, [int]$Seconds = 120) {
    $ErrorActionPreference = 'Continue'
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $BIN
    $psi.Arguments = (@('--config', $CONFIG) + $Arguments | ForEach-Object { ConvertTo-Arg $_ }) -join ' '
    $psi.WorkingDirectory = $Dir
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.StandardOutputEncoding = [Text.Encoding]::UTF8
    $psi.StandardErrorEncoding = [Text.Encoding]::UTF8
    $p = [Diagnostics.Process]::Start($psi)
    $out = $p.StandardOutput.ReadToEndAsync()
    $err = $p.StandardError.ReadToEndAsync()
    $timedOut = -not $p.WaitForExit($Seconds * 1000)
    if ($timedOut) {
        & taskkill.exe /T /F /PID $p.Id 2>&1 | Out-Null
    }
    $p.WaitForExit()
    [pscustomobject]@{ ExitCode = $p.ExitCode; Out = $out.Result; Err = $err.Result; TimedOut = $timedOut }
}

# DL runs dotlane, shows what it printed, and fails on a non-zero exit. DLTry leaves the exit code
# to the caller.
function DLTry {
    $r = Invoke-Dotlane -Arguments $args
    Write-Host ("`$ dotlane " + ($args -join ' '))
    if ($r.Out) { Write-Host $r.Out.TrimEnd() }
    if ($r.Err) { Write-Host $r.Err.TrimEnd() }
    $r
}
function DL {
    $r = DLTry @args
    if ($r.TimedOut) { Fail "dotlane $($args -join ' ') did not finish in 2 minutes (a prompt nobody can answer?)" }
    if ($r.ExitCode -ne 0) { Fail "dotlane $($args -join ' ') exited with $($r.ExitCode)" }
    $r
}

# CurlExe is curl.exe (in PowerShell 5.1, "curl" is an alias of Invoke-WebRequest) with the system's
# trust: it uses Schannel and so the Windows certificate store, with no --cacert. Dotlane's
# certificates name no revocation server, which Schannel otherwise reports as an error
# (CRYPT_E_NO_REVOCATION_CHECK); --ssl-revoke-best-effort lets that pass, like a browser does.
function CurlExe {
    $ErrorActionPreference = 'Continue' # PowerShell 5.1 turns a program's stderr into an error
    & curl.exe --ssl-revoke-best-effort -sS @args
}

# Checks is the list `dotlane doctor --json` prints, one object per check. PowerShell 5.1's
# ConvertFrom-Json writes a JSON array as one object, the whole array, so Where-Object would see
# every check at once; ForEach-Object unrolls it, and does nothing to PowerShell 7's output.
function ConvertFrom-Checks($out) {
    @(($out -join "`n") | ConvertFrom-Json | ForEach-Object { $_ })
}

function Get-DaemonLine {
    $r = DLTry doctor --json
    $checks = ConvertFrom-Checks $r.Out
    [string](@($checks | Where-Object { $_.name -eq 'Daemon' })[0].detail)
}

# Show what the system looks like, for a failed run.
function Show-Section($title, [scriptblock]$Body) {
    $ErrorActionPreference = 'Continue'
    Write-Host "`n--- $title"
    try { & $Body 2>&1 | Out-String -Width 200 | Write-Host } catch { Write-Host "($($_.Exception.Message))" }
}
function Show-Diagnostics {
    Show-Section 'reinstall: daemon before and after' { "before: $script:DaemonBefore"; "after:  $script:DaemonAfter" }
    Show-Section 'processes named dotlane or the service' {
        # Win32_Process, not Get-Process: it has the command line and the parent.
        Get-CimInstance Win32_Process | Where-Object { $_.Name -like '*dotlane*' -or $_.Name -like "$SERVICE*" } |
            Select-Object ProcessId, ParentProcessId, Name, CreationDate, ExecutablePath, CommandLine | Format-List
    }
    Show-Section 'the service folder' { Get-ChildItem (Split-Path $SERVICE_EXE) | Format-Table Name, Length, LastWriteTime }
    Show-Section 'daemon log' { if (Test-Path $LOG) { Get-Content $LOG -Tail 80 } else { "no log at $LOG" } }
    Show-Section 'scheduled task' { Get-ScheduledTask -TaskPath $TASK_PATH | Format-List * }
    Show-Section 'scheduled task info' { Get-ScheduledTask -TaskPath $TASK_PATH | Get-ScheduledTaskInfo | Format-List * }
    Show-Section 'scheduled task permissions' { (New-Object -ComObject Schedule.Service | ForEach-Object { $_.Connect(); $_.GetFolder('\Dotlane').GetTask($SERVICE).GetSecurityDescriptor(4) }) }
    Show-Section 'scheduled task xml' { & schtasks.exe /Query /TN "$TASK_PATH$SERVICE" /XML }
    Show-Section 'DNS rules (NRPT)' { Get-DnsClientNrptRule | Format-List Name, Namespace, NameServers, Comment }
    Show-Section 'Dotlane certificates, machine Root store' {
        Get-ChildItem Cert:\LocalMachine\Root | Where-Object { $_.Subject -like '*Dotlane*' } | Format-List Subject, Thumbprint, NotAfter
    }
    Show-Section 'Dotlane certificates, user Root store' {
        Get-ChildItem Cert:\CurrentUser\Root | Where-Object { $_.Subject -like '*Dotlane*' } | Format-List Subject, Thumbprint
    }
    Show-Section 'listening on 53, 80, 443, 18080' {
        Get-NetTCPConnection -State Listen -LocalPort 53, 80, 443, 18080 -ErrorAction SilentlyContinue | Format-Table LocalAddress, LocalPort, OwningProcess
        Get-NetUDPEndpoint -LocalPort 53 -ErrorAction SilentlyContinue | Format-Table LocalAddress, LocalPort, OwningProcess
    }
    Show-Section "lookup of web.$TLD" { Resolve-DnsName "web.$TLD" -ErrorAction SilentlyContinue | Format-Table }
    Show-Section "web.$TLD with curl" { CurlExe -v --max-time 10 "https://web.$TLD/hello.txt" 2>&1 }
    Show-Section 'dotlane doctor' { if (Test-Path $BIN) { (Invoke-Dotlane -Arguments @('doctor') -Seconds 60).Out } }
    Show-Section 'upstream log' { Get-Content (Join-Path $WORK 'upstream.log'), (Join-Path $WORK 'upstream.err') }
}

function Stop-Upstream {
    $ErrorActionPreference = 'Continue'
    if ($script:Upstream -and -not $script:Upstream.HasExited) {
        & taskkill.exe /T /F /PID $script:Upstream.Id 2>&1 | Out-Null
    }
}

# Test-Task says whether the service's scheduled task exists. (Windows PowerShell 5.1 treats what a
# program writes to stderr as an error, hence the setting.)
function Test-Task {
    $ErrorActionPreference = 'Continue'
    & schtasks.exe /Query /TN "$TASK_PATH$SERVICE" 2>&1 | Out-Null
    $LASTEXITCODE -eq 0
}

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal $id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Invoke-Test {
    New-Item -ItemType Directory -Force $WORK, $WWW | Out-Null
    Set-Content -Path $CONFIG -Encoding ascii -Value @"
tld = "$TLD"
service_name = "$SERVICE"
state_dir = '$STATE'
"@
    if (-not (Test-Admin)) {
        Write-Host 'Not elevated: dotlane will ask for permission (UAC), which nobody can answer on a CI runner.'
    }

    Step 'what holds the ports'
    # A runner can have IIS installed; stop it, as a developer would, so it does not hold 80/443.
    Get-Service W3SVC -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq 'Running' } | ForEach-Object {
        Write-Host 'stopping IIS (W3SVC)'; Stop-Service W3SVC -Force
    }
    Get-NetTCPConnection -State Listen -LocalPort 53, 80, 443 -ErrorAction SilentlyContinue | Format-Table LocalAddress, LocalPort, OwningProcess
    Get-NetUDPEndpoint -LocalPort 53 -ErrorAction SilentlyContinue | Format-Table LocalAddress, LocalPort, OwningProcess

    # Each build reports a release-like version, so the next command can tell that the service
    # runs an older one (an upgrade).
    function Build($version) {
        Remove-Item $BIN -ErrorAction SilentlyContinue
        & go build -ldflags "-X main.version=$version" -o $BIN ./cmd/dotlane
        if ($LASTEXITCODE -ne 0) { Fail "go build failed ($version)" }
    }
    Build '1.0.0-ci.1'

    Set-Content -Path (Join-Path $WWW 'hello.txt') -Value 'hello' -Encoding ascii
    $python = Get-Command python -ErrorAction SilentlyContinue
    $pyArgs = @()
    if (-not $python) { $python = Get-Command py -ErrorAction Stop; $pyArgs = @('-3') }
    $script:Upstream = Start-Process -FilePath $python.Source -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $WORK 'upstream.log') -RedirectStandardError (Join-Path $WORK 'upstream.err') `
        -ArgumentList ($pyArgs + @('-m', 'http.server', '18080', '--bind', '127.0.0.1', '--directory', "`"$WWW`""))
    # The test's dev server must answer before Dotlane is asked to proxy to it: Python can take
    # seconds to start on a cold runner, and a 502 then would be the test's fault, not Dotlane's.
    $up = $false
    for ($i = 0; $i -lt 60 -and -not $up; $i++) {
        CurlExe -f --max-time 1 http://127.0.0.1:18080/hello.txt -o NUL 2>$null
        $up = $LASTEXITCODE -eq 0
        if (-not $up) { Start-Sleep -Milliseconds 500 }
    }
    if (-not $up) { Fail "the test's dev server on :18080 did not start" }

    Step 'install'
    $start = Get-Date
    DL install | Out-Null

    Step 'first site'
    DL add web 18080 | Out-Null
    $body = (CurlExe -f --max-time 10 "https://web.$TLD/hello.txt") -join ''
    if ($LASTEXITCODE -ne 0) { Fail "https://web.$TLD/hello.txt was not served" }
    if ($body.Trim() -ne 'hello') { Fail "unexpected body: $body" }
    $elapsed = [int]((Get-Date) - $start).TotalSeconds
    Write-Host "install -> first site: ${elapsed}s"
    if ($elapsed -ge 90) { Fail "install -> first site took ${elapsed}s (budget 90s)" }

    Step 'HTTP redirect, unknown name, dev server down'
    $loc = (CurlExe -o NUL -w '%{redirect_url}' "http://web.$TLD/x") -join ''
    if ($loc -ne "https://web.$TLD/x") { Fail "redirect went to $loc" }
    $code = (CurlExe -o NUL -w '%{http_code}' "https://nosuch.$TLD/") -join ''
    if ($code -ne '404') { Fail "unknown name answered $code, want the no-site page" }
    DL add down 18081 | Out-Null
    $code = (CurlExe -o NUL -w '%{http_code}' "https://down.$TLD/") -join ''
    if ($code -ne '502') { Fail "stopped dev server answered $code" }
    DL rm down | Out-Null

    Step 'other machines are dropped'
    # Dotlane listens on 127.0.0.1 only on Windows, so another address of this PC gets no answer.
    $lan = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } |
        Select-Object -First 1 -ExpandProperty IPAddress
    if ($lan) {
        CurlExe -o NUL --max-time 5 --resolve "web.${TLD}:443:$lan" "https://web.$TLD/" 2>$null
        if ($LASTEXITCODE -eq 0) { Fail "a request to $lan was answered" }
        Write-Host "request to $lan got no answer"
    } else {
        Write-Host 'no network address; skipped'
    }

    Step 'project file'
    $project = Join-Path $WORK 'project'
    New-Item -ItemType Directory -Force (Join-Path $project 'sub') | Out-Null
    Set-Content -Path (Join-Path $project 'dotlane.toml') -Encoding ascii -Value "[sites.proj]`nport = 18080"
    $r = Invoke-Dotlane -Arguments @('up') -Dir (Join-Path $project 'sub')
    Write-Host $r.Out
    if ($r.ExitCode -ne 0) { Fail "dotlane up failed: $($r.Err)" }
    CurlExe -f --max-time 10 "https://proj.$TLD/hello.txt" -o NUL
    if ($LASTEXITCODE -ne 0) { Fail 'dotlane up did not serve proj' }
    $r = Invoke-Dotlane -Arguments @('down') -Dir $project
    if ($r.ExitCode -ne 0) { Fail "dotlane down failed: $($r.Err)" }
    if ((DL ls --json).Out -match '"proj"') { Fail 'dotlane down left proj' }

    Step 'reinstall over the running daemon'
    # What an upgrade does: rebuild, then install again while the daemon runs. Install must copy the
    # new build to the service's folder (the running copy is renamed aside, Windows will not
    # overwrite it), restart the daemon into it, and keep every site.
    $before = Get-DaemonLine
    $script:DaemonBefore = $before
    Write-Host "before: $before"
    Build '1.0.0-ci.2'
    $r = DLTry install
    if ($r.ExitCode -ne 0) { Fail 'reinstall failed' }
    if ($r.Out -notmatch [regex]::Escape('restarted (to load the rebuilt binary)')) { Fail 'install did not restart the daemon' }
    $after = Get-DaemonLine
    $script:DaemonAfter = $after
    Write-Host "after:  $after"
    if ($after -notlike '*1.0.0-ci.2*') { Fail 'the daemon does not run the new build' }
    $pidBefore = [regex]::Match($before, 'pid (\d+)').Groups[1].Value
    $pidAfter = [regex]::Match($after, 'pid (\d+)').Groups[1].Value
    if ($pidBefore -eq $pidAfter) { Fail "daemon pid unchanged ($pidAfter)" }
    if ((Get-FileHash $BIN).Hash -ne (Get-FileHash $SERVICE_EXE).Hash) { Fail "the service's copy is not the new build" }
    $body = (CurlExe -f --max-time 10 "https://web.$TLD/hello.txt") -join ''
    if ($LASTEXITCODE -ne 0) { Fail "web.$TLD not served after the reinstall" }
    if ($body.Trim() -ne 'hello') { Fail "unexpected body after the reinstall: $body" }
    if ((DL ls --json).Out -notmatch '"web"') { Fail 'site web lost in the reinstall' }
    $r = DLTry install
    if ($r.ExitCode -ne 0) { Fail 'second install failed' }
    if ($r.Out -match 'restarted') { Fail 'an unchanged binary restarted the daemon' }
    Write-Host "restarted into the new build (pid $pidBefore -> $pidAfter); an unchanged install left it alone"

    Step 'install --refresh after an upgrade'
    # What a user runs after an upgrade: just the new build, no permission prompt for the service.
    $before = Get-DaemonLine
    Build '1.0.0-ci.3'
    $r = DLTry install --refresh
    if ($r.ExitCode -ne 0) { Fail 'install --refresh failed' }
    if ($r.Out -notmatch [regex]::Escape('restarted (to load the rebuilt binary)')) { Fail 'install --refresh did not restart the daemon' }
    $after = Get-DaemonLine
    if ($after -notlike '*1.0.0-ci.3*') { Fail "the daemon does not run the refreshed build: $after" }
    if ([regex]::Match($before, 'pid (\d+)').Groups[1].Value -eq [regex]::Match($after, 'pid (\d+)').Groups[1].Value) { Fail 'daemon pid unchanged after refresh' }
    CurlExe -f --max-time 10 "https://web.$TLD/hello.txt" -o NUL
    if ($LASTEXITCODE -ne 0) { Fail "web.$TLD not served after the refresh" }

    Step 'the next command after an upgrade restarts the service'
    # A package manager replaces the binary but cannot restart the service; the next add, rm, ls,
    # up or down notices that the daemon runs an older release and does it.
    $before = Get-DaemonLine
    Build '1.0.0-ci.4'
    $r = DL ls --json
    if ($r.Err -notmatch 'Updating the Dotlane service') { Fail "ls did not update the service; it printed: $($r.Err)" }
    if ($r.Out -notmatch '"web"') { Fail 'ls lost the site web' }
    $after = Get-DaemonLine
    if ($after -notlike '*1.0.0-ci.4*') { Fail "the daemon does not run the upgraded build: $after" }
    if ([regex]::Match($before, 'pid (\d+)').Groups[1].Value -eq [regex]::Match($after, 'pid (\d+)').Groups[1].Value) { Fail 'daemon pid unchanged after the automatic upgrade' }
    CurlExe -f --max-time 10 "https://web.$TLD/hello.txt" -o NUL
    if ($LASTEXITCODE -ne 0) { Fail "web.$TLD not served after the automatic upgrade" }

    Step 'domain endings'
    # A second ending answers at once, can become the primary, and the first can go: its NRPT rule
    # goes with it, and the sites stay, on the new ending.
    $TLD2 = 'dltest2'
    function Get-OurNamespaces { @(Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $RULE_COMMENT } | ForEach-Object { $_.Namespace }) }
    DL tld add $TLD2 | Out-Null
    if ((Get-OurNamespaces) -notcontains ".$TLD2") { Fail "no NRPT rule for .$TLD2 after tld add" }
    $body = (CurlExe -f --max-time 10 "https://web.$TLD2/hello.txt") -join ''
    if ($LASTEXITCODE -ne 0) { Fail "web.$TLD2 not served after tld add" }
    if ($body.Trim() -ne 'hello') { Fail "unexpected body on .${TLD2}: $body" }
    DL tld use $TLD2 | Out-Null
    $web = @(((DL ls --json).Out -join "`n") | ConvertFrom-Json | ForEach-Object { $_ }) | Where-Object { $_.name -eq 'web' }
    if (-not $web -or $web.urls[0] -notlike "https://web.$TLD2*") { Fail "the primary URL is not on .${TLD2}: $($web.urls -join ', ')" }
    DL tld rm $TLD | Out-Null
    if ((Get-OurNamespaces) -contains ".$TLD") { Fail "the NRPT rule for .$TLD is left after tld rm" }
    CurlExe -f --max-time 10 "https://web.$TLD2/hello.txt" -o NUL
    if ($LASTEXITCODE -ne 0) { Fail "web.$TLD2 not served after removing .$TLD" }
    # Back to the first ending, so the steps below test what they always did.
    DL tld use $TLD | Out-Null
    DL tld rm $TLD2 | Out-Null
    if ((Get-OurNamespaces) -contains ".$TLD2") { Fail "the NRPT rule for .$TLD2 is left after tld rm" }
    CurlExe -f --max-time 10 "https://web.$TLD/hello.txt" -o NUL
    if ($LASTEXITCODE -ne 0) { Fail "web.$TLD not served after switching back" }
    Write-Host 'added, served, made primary, removed, and back'

    Step 'doctor'
    $r = DLTry doctor --json
    $checks = ConvertFrom-Checks $r.Out
    $bad = @($checks | Where-Object { $_.status -eq 'fail' })
    if ($bad.Count -gt 0) { Fail ('doctor reports: ' + (($bad | ForEach-Object { "$($_.name): $($_.detail)" }) -join '; ')) }
    # Firefox, when installed (runners have it), gets a warning-level hint; nothing else may warn.
    $warn = @($checks | Where-Object { $_.status -eq 'warn' -and $_.name -ne 'Firefox' })
    if ($warn.Count -gt 0) { Fail ('doctor warns: ' + (($warn | ForEach-Object { "$($_.name): $($_.detail)" }) -join '; ')) }
    foreach ($name in 'Login service', 'Daemon', 'Certificate authority', "DNS for .$TLD", "Lookup .$TLD", 'Private files') {
        $c = $checks | Where-Object { $_.name -eq $name }
        if (-not $c -or $c.status -ne 'ok') { Fail "doctor check '$name' is not ok" }
    }
    DL doctor | Out-Null

    Step 'what install put on the system'
    if (-not (Test-Task)) { Fail 'the scheduled task is missing' }
    $rules = @(Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $RULE_COMMENT })
    if ($rules.Count -ne 1 -or $rules[0].Namespace -notcontains ".$TLD") { Fail "no single NRPT rule for .$TLD with the comment $RULE_COMMENT" }
    if (-not (Get-ChildItem Cert:\LocalMachine\Root | Where-Object { $_.Subject -like '*Dotlane Local Authority*' })) {
        Fail 'the CA is not in the machine Root store'
    }

    Step 'uninstall --purge'
    $r = DLTry uninstall --purge
    if ($r.TimedOut) { Fail 'uninstall waited for an answer nobody gave (killed after 2 minutes)' }
    if ($r.ExitCode -ne 0) { Fail 'uninstall failed' }
    if (Test-Task) { Fail 'login service still registered' }
    if (@(Get-ScheduledTask -TaskPath "$TASK_PATH*" -ErrorAction SilentlyContinue).Count -gt 0) { Fail 'a task is left in \Dotlane\' }
    if (@(Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $RULE_COMMENT }).Count -gt 0) { Fail "DNS rule for .$TLD left behind" }
    if (Get-ChildItem Cert:\LocalMachine\Root | Where-Object { $_.Subject -like '*Dotlane Local Authority*' }) { Fail 'CA still in the machine Root store' }
    if (Get-ChildItem Cert:\CurrentUser\Root | Where-Object { $_.Subject -like '*Dotlane Local Authority*' }) { Fail 'CA still in the user Root store' }
    if (Test-Path $SERVICE_EXE) { Fail "the service's binary left behind" }
    if (Test-Path "$SERVICE_EXE.old") { Fail "the service's old binary left behind" }
    if (Test-Path $STATE) { Fail 'state left behind' }
    if (Test-Path $LOG) { Fail 'log left behind' }
    if (Get-Process -Name $SERVICE -ErrorAction SilentlyContinue) { Fail 'the daemon is still running' }

    Step 'install --refresh with nothing installed'
    $r = DLTry install --refresh
    if ($r.ExitCode -ne 0) { Fail 'install --refresh failed when not installed' }
    if ($r.Out -notmatch 'nothing to refresh') { Fail 'install --refresh did not say there was nothing to do' }
    if (Test-Task) { Fail 'install --refresh installed a service' }
    if (Test-Path $SERVICE_EXE) { Fail 'install --refresh copied the binary' }
    Write-Host 'no-op, as expected'
}

$failed = $false
try {
    Invoke-Test
} catch {
    $failed = $true
    Write-Host "`nFAIL: $($_.Exception.Message)"
    Write-Host $_.ScriptStackTrace
    Show-Diagnostics
}
Stop-Upstream
if ($failed) { exit 1 }
Remove-Item -Recurse -Force $WORK -ErrorAction SilentlyContinue
Step 'passed'
