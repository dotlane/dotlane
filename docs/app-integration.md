# App integration

Serving an app under a new hostname, behind a proxy, over HTTPS touches four things in the app.
All four came up with MyApp (Vite + React frontend, FastAPI backend behind Vite's `/api`
proxy).

## 1. Host-header allowlists

Dev servers increasingly refuse unknown `Host` headers (DNS-rebinding protection). Vite answers
`Blocked request. This host is not allowed.` until told otherwise:

```ts
// vite.config.ts
server: { allowedHosts: ['.traix'] }   // leading dot: every *.traix name
```

Equivalents elsewhere: Django `ALLOWED_HOSTS`, Rails `config.hosts`, webpack-dev-server
`allowedHosts`, Next.js `allowedDevOrigins`.

## 2. Origin / CSRF checks

Backends that check the `Origin` header on state-changing requests must list the new origin.
MyApp's FastAPI backend refuses sign-in from an unlisted origin with
`403 {"detail":"Cross-origin request rejected.","code":"auth.cross_origin"}`:

```env
# backend/.env
ALLOWED_ORIGINS=https://myapp.traix,http://localhost:5173
```

The backend reads this at start-up, so the dev server must be restarted. MyApp also uses the
first entry as the base for links in emails.

## 3. Cookies and HTTPS

Being on real HTTPS is a feature: `Secure` cookies, `SameSite=None`, service workers, the
clipboard API and other secure-context-only features behave as in production. The browser reports
`window.isSecureContext === true` on `https://myapp.traix`. Caddy adds `X-Forwarded-Proto:
https` and `X-Forwarded-For` to proxied requests; a backend that derives the scheme or client IP
from them must trust the proxy's address (`127.0.0.1`).

## 4. Live reload (WebSockets)

Caddy proxies WebSockets with no configuration. Vite's client connects back to the page's own
host and port, so live reload works through `https://myapp.traix` unchanged (verified: the
browser logs `[vite] connected.`).

## What stays the same

- The dev server's own address (`localhost:5173`) keeps working alongside.
- Server-to-server proxies inside the app (Vite → `127.0.0.1:8000`) are untouched.
- Mobile/emulator clients are unaffected: they cannot resolve `.traix` and keep using their own
  addresses.

## Checklist for a new app

1. `dotlane add <name> <port>`.
2. Allow the host in the dev server (step 1).
3. Add `https://<name>.traix` to any origin/CORS/CSRF allowlist (step 2); restart the backend.
4. OAuth apps: register `https://<name>.traix/...` as a redirect URI.
5. Open the full `https://<name>.traix` once (Chrome treats a bare unknown domain ending as a
   search).
