# KHoist documentation

The user documentation is an Astro/Starlight site intended for `khoist.devantler.tech`.
Pages live in `src/content/docs/`; downloadable examples live in `public/examples/`.

## Preview and validate

Use Node.js 24 or newer:

```bash
npm ci --prefix website
npm run build --prefix website
npm run preview --prefix website -- --host 127.0.0.1
```

Open the local address printed by the preview command. Check navigation, search,
keyboard focus, narrow screens and both themes after changing layout or content.
The build checks internal routes and fragments and generates the search index.

Check the downloadable declaration with the product's real offline checker:

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check \
  --file website/public/examples/harbour-product.yaml
```

Run the repository's link checker after building: its root is `website/dist`.
CI builds the site for pull requests. The documentation workflow publishes only
from `main`; GitHub Pages, DNS and HTTPS must be configured before the public
address can serve the site.

## Keep the guides accurate

Give each page a user task or a specific reference purpose. Start tutorials with
prerequisites and end with an observable result and cleanup. Check commands against
the current CLI, chart and API; state when a feature is optional or a provider path
has only controlled evidence. Retained implementation identifiers remain explicit
until the corresponding migration has shipped. Link to detailed repository
contracts rather than copying them into a second reference.
