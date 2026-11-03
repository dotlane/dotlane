# dotlane

Real, trusted HTTPS addresses for local dev servers: `https://myapp.dotlane` instead of
`http://localhost:5173`. Any language, any framework, one command.

```bash
npx dotlane install          # once: login service, trusted local CA, DNS for .dotlane
npx dotlane add myapp 5173   # https://myapp.dotlane → localhost:5173
```

Or in a project, list its sites in a committed `dotlane.toml` and add Dotlane as a dev
dependency:

```toml
[sites.myapp]
port = 5173
```

```bash
npm install --save-dev dotlane
npx dotlane up
```

macOS, Linux and Windows, on x64 and arm64 (Linux needs systemd-resolved). This package is a
small Node launcher: npm installs the binary for your machine from one of the `@dotlane/*`
platform packages (`darwin`, `linux` or `win32`, each `x64` or `arm64`), with no install
scripts. `dotlane install` copies the binary out of
`node_modules`, so the service keeps running whatever happens to the project.

Documentation, source and licence (Apache-2.0): https://github.com/dotlane/dotlane

If Dotlane saves you time, you can [buy me a coffee](https://www.buymeacoffee.com/pedrodee).
