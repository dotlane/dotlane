# App integration

Serving an app under a new hostname, behind a proxy, over HTTPS touches four things in the app.
All four came up with the reference app (Vite + React frontend, FastAPI backend behind Vite's `/api`
proxy). The general rules come first; [Framework guides](#framework-guides) has the settings for
Vite, Next.js, Django, FastAPI, Rails, Laravel, ASP.NET Core and Spring Boot.

## What the app receives

Dotlane's proxy is Caddy with its defaults, so every request reaches the dev server:

- over plain HTTP on `localhost:<port>`, from `127.0.0.1` (or `::1`);
- with the **`Host` header unchanged** (`myapp.dotlane`), so the app sees its real name;
- with **`X-Forwarded-Proto: https`**, `X-Forwarded-For` (the browser's address) and
  `X-Forwarded-Host`.

An app that builds absolute URLs, redirects to HTTPS, or marks cookies `Secure` from the request's
scheme must trust these headers from `127.0.0.1`, or it thinks it is on `http://` (wrong links,
redirect loops, cookies it refuses to set). Most frameworks have one setting for this; the guides
below name it.

## 1. Host-header allowlists

Dev servers increasingly refuse unknown `Host` headers (DNS-rebinding protection). Vite answers
`Blocked request. This host is not allowed.` until told otherwise:

```ts
// vite.config.ts
server: { allowedHosts: ['.dotlane'] }   // leading dot: every *.dotlane name
```

Equivalents elsewhere: Django `ALLOWED_HOSTS`, Rails `config.hosts`, webpack-dev-server
`allowedHosts`, Next.js `allowedDevOrigins`, ASP.NET Core `AllowedHosts`.

## 2. Origin / CSRF checks

Backends that check the `Origin` header on state-changing requests must list the new origin.
The reference app's FastAPI backend refuses sign-in from an unlisted origin with
`403 {"detail":"Cross-origin request rejected.","code":"auth.cross_origin"}`:

```env
# backend/.env
ALLOWED_ORIGINS=https://myapp.dotlane,http://localhost:5173
```

The backend reads this at start-up, so the dev server must be restarted. The reference app also uses the
first entry as the base for links in emails.

Frameworks that compare `Origin` with the request's own scheme and host pass without a list once
they trust `X-Forwarded-Proto` (see [What the app receives](#what-the-app-receives)); a frontend
on another name (`myapp.dotlane` calling `myapp-api.dotlane`) always needs CORS.

## 3. Cookies and HTTPS

Being on real HTTPS is a feature: `Secure` cookies, `SameSite=None`, service workers, the
clipboard API and other secure-context-only features behave as in production. The browser reports
`window.isSecureContext === true` on `https://myapp.dotlane`. Caddy adds `X-Forwarded-Proto:
https` and `X-Forwarded-For` to proxied requests; a backend that derives the scheme or client IP
from them must trust the proxy's address (`127.0.0.1`).

**Two names are two sites.** A Dotlane name has no dots (`myapp`, `myapp-api`), and browsers treat
an unknown ending like `.dotlane` as a public suffix. So `myapp.dotlane` and `myapp-api.dotlane`
are different *sites*, not two subdomains of one:

- a cookie cannot be shared between them (`Domain=.dotlane` is rejected);
- a `SameSite=Lax` or `Strict` cookie set by `myapp-api.dotlane` is not sent on `fetch` calls
  from `myapp.dotlane`.

Two ways to deal with it, best first:

1. **One name, proxied API.** Serve the frontend on one name and proxy `/api` to the backend from
   the frontend's dev server (as the reference app does with Vite's `server.proxy`). Same origin,
   no CORS, cookies as in production.
2. **Two names, cross-site cookies.** Mark the API's session cookie `SameSite=None; Secure`, allow
   the frontend's origin with credentials in CORS, and send `credentials: 'include'` from the
   frontend. This mirrors a production setup where the API is on another site.

## 4. Live reload (WebSockets)

Caddy proxies WebSockets with no configuration. Vite's client connects back to the page's own
host and port, so live reload works through `https://myapp.dotlane` unchanged (verified: the
browser logs `[vite] connected.`).

## Framework guides

Each guide uses `myapp` for the app (or its frontend) and `myapp-api` for a separate backend. Only
the development settings change: keep them in the framework's development config or `.env`, not in
production's.

### Vite

```ts
// vite.config.ts
export default defineConfig({
  server: {
    allowedHosts: ['.dotlane'],                         // host allowlist (Vite 5.4.12+ / 6.0.9+)
    proxy: { '/api': 'http://127.0.0.1:8000' },        // backend on the same name: no CORS
  },
})
```

- **Host:** `server.allowedHosts`; without it Vite answers `Blocked request`.
- **Origins:** none for the page itself. When another site loads the dev server's scripts (a
  backend-rendered page, as with Laravel or Rails), list it in `server.cors`, which since 6.0.9
  allows only `localhost` origins: `cors: { origin: /^https:\/\/[\w-]+\.dotlane$/ }`.
- **Cookies:** set by the backend; with the `/api` proxy they are first-party.
- **Live reload:** works as is (section 4).

### Next.js

```js
// next.config.js
module.exports = {
  allowedDevOrigins: ['*.dotlane'],   // Next.js 15.2+
}
```

- **Host:** `allowedDevOrigins` lets the browser load the dev server's own resources (`/_next/*`,
  live reload) from a name other than `localhost`; without it they are blocked or warned about,
  depending on the version.
- **Origins / CSRF:** Server Actions compare `Origin` with `Host`, which Dotlane keeps, so they
  work on the site's own name. Only a form posted from another name needs
  `experimental.serverActions.allowedOrigins: ['*.dotlane']`.
- **Cookies:** Auth.js (NextAuth) needs the public URL and to trust the proxy:
  `AUTH_URL=https://myapp.dotlane` and `AUTH_TRUST_HOST=true` (`NEXTAUTH_URL` in v4). On HTTPS
  its cookies get the `__Secure-` prefix, as in production.

### Django

```python
# settings.py (development)
ALLOWED_HOSTS = ['.dotlane', 'localhost', '127.0.0.1']
CSRF_TRUSTED_ORIGINS = ['https://*.dotlane']
SECURE_PROXY_SSL_HEADER = ('HTTP_X_FORWARDED_PROTO', 'https')
SESSION_COOKIE_SECURE = True
CSRF_COOKIE_SECURE = True
```

- **Host:** `ALLOWED_HOSTS`; the leading dot matches every name under `.dotlane`. Without it,
  `Invalid HTTP_HOST header` (400).
- **Origins / CSRF:** `SECURE_PROXY_SSL_HEADER` makes `request.is_secure()` true, so Django's
  `Origin` check matches the site's own name; `CSRF_TRUSTED_ORIGINS` (scheme required, Django 4+)
  covers forms and `fetch` calls from other `.dotlane` names. Without either, posts fail with
  `CSRF verification failed. Origin checking failed`.
- **CORS** (frontend on another name), with `django-cors-headers`:
  `CORS_ALLOWED_ORIGIN_REGEXES = [r'^https://[\w-]+\.dotlane$']` and
  `CORS_ALLOW_CREDENTIALS = True`; for cookies across the two names see section 3
  (`SESSION_COOKIE_SAMESITE = 'None'`).
- **Cookies:** the `*_SECURE` settings are optional, but match production.
- Only set `SECURE_PROXY_SSL_HEADER` where the header comes from a trusted proxy: in
  development that is Dotlane, on `127.0.0.1`.

### FastAPI

```python
from fastapi.middleware.cors import CORSMiddleware
from starlette.middleware.trustedhost import TrustedHostMiddleware

app.add_middleware(TrustedHostMiddleware, allowed_hosts=['*.dotlane', 'localhost', '127.0.0.1'])
app.add_middleware(
    CORSMiddleware,
    allow_origin_regex=r'https://[\w-]+\.dotlane',
    allow_credentials=True,
    allow_methods=['*'],
    allow_headers=['*'],
)
```

- **Host:** FastAPI checks nothing unless the app uses `TrustedHostMiddleware`; then add
  `*.dotlane`.
- **Proxy headers:** Uvicorn trusts `X-Forwarded-Proto` and `X-Forwarded-For` from `127.0.0.1` by
  default (`--proxy-headers`, `--forwarded-allow-ips`), so `request.url` is `https://…` and
  `request.client` is the browser. An app bound to `::1` or `0.0.0.0` behind Dotlane may need
  `--forwarded-allow-ips='127.0.0.1,::1'`.
- **Origins:** `CORSMiddleware` only when the frontend is on another name; an app-level `Origin`
  check (like the reference app's `ALLOWED_ORIGINS`) needs `https://myapp.dotlane` added.
- **Cookies:** `response.set_cookie(..., secure=True, samesite='lax')`; `samesite='none'` for the
  two-name setup in section 3.

### Rails

```ruby
# config/environments/development.rb
config.hosts << '.dotlane'
config.action_cable.allowed_request_origins = [%r{\Ahttps://[\w-]+\.dotlane\z}]
```

- **Host:** `config.hosts`; the leading dot allows every name under `.dotlane`. Without it, the
  `Blocked hosts` page.
- **Origins / CSRF:** Rails compares `Origin` with `request.base_url`, which follows
  `X-Forwarded-Proto` from `127.0.0.1`, so forms and `fetch` with the CSRF token work on the
  site's own name. Action Cable checks origins separately: add the line above.
- **CORS** (frontend on another name), with `rack-cors`:
  `origins %r{\Ahttps://[\w-]+\.dotlane\z}` and `credentials: true`.
- **Cookies:** the session cookie is `Secure` when the request is HTTPS; for the two-name setup
  use `same_site: :none` in `config.session_store`.

### Laravel

Until Dotlane serves PHP itself ([roadmap](roadmap.md)), run Laravel on a port:
`php artisan serve --port=8000`, then `dotlane add myapp 8000`.

```env
# .env
APP_URL=https://myapp.dotlane
SESSION_SECURE_COOKIE=true
```

```php
// bootstrap/app.php (Laravel 11+)
->withMiddleware(function (Middleware $middleware) {
    $middleware->trustProxies(at: '127.0.0.1');
})
```

- **Host:** Laravel checks nothing by default. If the app turns on `TrustHosts`, add the ending:
  `$middleware->trustHosts(at: ['dotlane'])` (subdomains are included).
- **Proxy headers:** `trustProxies` makes `url()`, `route()` and redirects use `https://`; without
  it, pages link to `http://myapp.dotlane` and the browser blocks mixed content.
- **Origins / CSRF:** `VerifyCsrfToken` uses a token, not `Origin`, so forms work as is. Sanctum
  SPA auth needs the frontend's name: `SANCTUM_STATEFUL_DOMAINS=myapp.dotlane`, and it must be the
  same site as the API (section 3), so serve the SPA from Laravel or proxy `/api` to it.
- **Vite assets:** the page on `https://myapp.dotlane` cannot load scripts from
  `http://localhost:5173` (mixed content). Give the Vite dev server its own name,
  `dotlane add myapp-vite 5173`, and point the plugin at it:

  ```js
  // vite.config.js
  server: {
    allowedHosts: ['.dotlane'],
    origin: 'https://myapp-vite.dotlane',
    hmr: { host: 'myapp-vite.dotlane', protocol: 'wss', clientPort: 443 },
    cors: { origin: /^https:\/\/[\w-]+\.dotlane$/ },
  },
  ```

  `public/hot` should then hold `https://myapp-vite.dotlane`.

### ASP.NET Core

Run the `http` launch profile (`dotnet run --launch-profile http`) and add its port; Dotlane is
the HTTPS end, so the app's own `https` profile and dev certificate are not needed.

```csharp
// Program.cs
builder.Services.Configure<ForwardedHeadersOptions>(o =>
    o.ForwardedHeaders = ForwardedHeaders.XForwardedFor | ForwardedHeaders.XForwardedProto);

var app = builder.Build();
app.UseForwardedHeaders();   // before UseHttpsRedirection, auth and anything that builds URLs
app.UseHttpsRedirection();
```

- **Host:** `AllowedHosts` in `appsettings.json` is `*` in the templates; if it is narrowed, add
  `*.dotlane` (`"AllowedHosts": "localhost;*.dotlane"`).
- **Proxy headers:** `UseForwardedHeaders` trusts loopback by default, so `Request.Scheme` is
  `https`. Without it, `UseHttpsRedirection` redirects every request to the app's own HTTPS port.
  Setting `ASPNETCORE_FORWARDEDHEADERS_ENABLED=true` does the same without code.
- **Origins / CSRF:** antiforgery uses tokens, so forms and Blazor work as is. CORS for a
  frontend on another name:
  `p.WithOrigins("https://*.dotlane").SetIsOriginAllowedToAllowWildcardSubdomains().AllowCredentials().AllowAnyHeader().AllowAnyMethod()`.
- **Cookies:** with forwarded headers on, `CookieSecurePolicy.SameAsRequest` marks cookies
  `Secure`; for the two-name setup set `SameSite = SameSiteMode.None`.

### Spring Boot

```properties
# application-dev.properties
server.forward-headers-strategy=native
server.servlet.session.cookie.secure=true
```

- **Host:** Spring Boot checks nothing.
- **Proxy headers:** `forward-headers-strategy=native` makes the embedded server trust
  `X-Forwarded-*` from `127.0.0.1`, so redirects, `{baseUrl}` and OAuth 2 login redirect URIs use
  `https://myapp.dotlane`. Without it, OAuth providers reject `http://…/login/oauth2/code/…`.
- **Origins / CSRF:** Spring Security's CSRF protection uses tokens, so forms work as is. CORS for
  a frontend on another name:

  ```java
  var cors = new CorsConfiguration();
  cors.setAllowedOriginPatterns(List.of("https://*.dotlane"));
  cors.setAllowCredentials(true);
  cors.setAllowedMethods(List.of("*"));
  cors.setAllowedHeaders(List.of("*"));
  ```

- **Cookies:** `server.servlet.session.cookie.same-site=none` for the two-name setup.

## What stays the same

- The dev server's own address (`localhost:5173`) keeps working alongside.
- Server-to-server proxies inside the app (Vite → `127.0.0.1:8000`) are untouched.
- Mobile/emulator clients are unaffected: they cannot resolve `.dotlane` and keep using their own
  addresses.

## Checklist for a new app

1. `dotlane add <name> <port>`.
2. Allow the host in the dev server (step 1).
3. Trust `X-Forwarded-Proto` from `127.0.0.1` if the app builds URLs or redirects to HTTPS
   ([What the app receives](#what-the-app-receives)).
4. Add `https://<name>.dotlane` to any origin/CORS/CSRF allowlist (step 2); restart the backend.
5. Frontend and API on two names: proxy the API through one name, or use `SameSite=None` cookies
   (step 3).
6. OAuth apps: register `https://<name>.dotlane/...` as a redirect URI.
7. Open the full `https://<name>.dotlane` once (Chrome treats a bare unknown domain ending as a
   search).
