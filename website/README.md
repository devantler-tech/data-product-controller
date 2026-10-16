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

The dependency overrides retain patched build tooling while upstream dependencies
catch up. The selector-parser pin addresses
[GHSA-rj75-hqrm-r3gf](https://github.com/advisories/GHSA-rj75-hqrm-r3gf);
remove it when the upstream code-highlighting dependency selects a patched version.

Check the downloadable declaration with the product's real offline checker:

```bash
PUBLISHER_PREFLIGHT_ENABLED=true go run ./cmd/product-check \
  --file website/public/examples/harbour-product.yaml
```

Run the repository's link checker after building:

```bash
bash scripts/documentation-links.sh website/src/content/docs website/public/robots.txt
```

Its root is `website/dist`; canonical site URLs resolve against that generated
content, including the sitemap. Other domains retain normal network checks.
CI builds the site for pull requests. The documentation workflow publishes only
from `main`; GitHub Pages, DNS and HTTPS must be configured before the public
address can serve the site. For Actions publishing, GitHub ignores the checked-in
`CNAME` file: the Pages custom domain must also be configured in GitHub.

The repository settings owner is
[`devantler-tech/.github`](https://github.com/devantler-tech/.github/issues/614).
The desired Pages settings are `buildType: workflow` and
`cname: khoist.devantler.tech`, with no branch source. The current GitHub App cannot
create a Pages site; a maintainer-approved one-time bootstrap must establish the
site before the declaration can preserve it. Verify the live settings and the
owner's reconciliation separately from content publication.

Configure the Pages custom domain before DNS. The DNS target is a DNS-only CNAME
from `khoist.devantler.tech` to `devantler-tech.github.io`. The
[platform ExternalDNS integration](https://github.com/devantler-tech/platform/issues/4939)
reconciles `deploy/documentation/` from this repository's signed manifest artifact
through a product-scoped tenant identity. The product owns the DNS declaration;
the platform owns the tenant registration and shared writer. Its admission
boundary must be deployed and
verified before CRD discovery is enabled on the existing writer. Keep the current
TXT ownership, zone filters and routed-service discovery. Do not create a second
DNS writer or change the shared Gateway's target for this static site.
After publication, read the Pages deployment, DNS, HTTPS certificate and a working
tutorial/search path at the public URL before calling the site live.

## Keep the guides accurate

Give each page a user task or a specific reference purpose. Start tutorials with
prerequisites and end with an observable result and cleanup. Check commands against
the current CLI, chart and API; state when a feature is optional or a provider path
has only controlled evidence. Retained implementation identifiers remain explicit
until the corresponding migration has shipped. Link to detailed repository
contracts rather than copying them into a second reference.
