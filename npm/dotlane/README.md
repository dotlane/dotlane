# dotlane

Real, trusted HTTPS addresses for local dev servers: `https://myapp.traix` instead of
`http://localhost:5173`. Any language, any framework, one command.

```bash
npx dotlane install          # once: login service, trusted local CA, DNS for .traix
npx dotlane add myapp 5173   # https://myapp.traix → localhost:5173
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

macOS (Apple Silicon and Intel) for now; Linux and Windows are planned. This package is a small
Node launcher: npm installs the binary for your machine from `@dotlane/darwin-arm64` or
`@dotlane/darwin-x64`, with no install scripts. `dotlane install` copies the binary out of
`node_modules`, so the service keeps running whatever happens to the project.

Documentation, source and licence (Apache-2.0): https://github.com/dotlane/dotlane
