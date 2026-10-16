# Product identity

KHoist uses an orange harbour crane hoisting connected data-product blocks. The
crane gives the name a visible action; the distinct blocks and their links
represent independently owned products and composition. This is a brand metaphor,
not an architecture diagram: the controller handles metadata, not product data.

## Assets

- [Harbour illustration](../website/src/assets/khoist-harbour.png): the original
  landscape artwork for the repository introduction and documentation overview.
  Keep the complete crane, suspended products and connecting ropes visible.
- [Compact mark](../website/public/brand/khoist-mark.svg): an editable vector for
  the documentation header, favicon and small product identifiers. Its ink
  background keeps the orange crane and white blocks legible in either theme.

Keep the product name as **KHoist** in surrounding text rather than baking it into
the illustration. Use the compact mark at small sizes; do not reduce the detailed
illustration to an icon or replace technical diagrams with it. Provide descriptive
alternative text for the illustration. The header mark sits beside the product
name and does not need a duplicate spoken label.

The core palette is signal orange `#f97316`, pale orange `#fdba74`, warm white
`#fff8ee`, ink `#101c2a` and slate `#334155`. Interface text continues to use
the documentation's existing contrast-adjusted light/dark tokens. The wordmark
uses the existing Bricolage Grotesque heading face; body copy uses Hanken Grotesque.
Avoid adding decorative motion or changing these assets' colours independently.

## Artwork provenance

The original illustration was created with the built-in image-generation tool,
with KSail's textured nautical artwork as a visual reference, then visually
reviewed. No KSail image is distributed by this repository. The compact SVG is
authored source. Both assets are distributed under this repository's
[Apache-2.0 license](../LICENSE).

The illustration prompt is retained in [the generation brief](product-identity-prompt.txt)
for reproducibility. Astro generates responsive WebP renditions from the original
PNG during the documentation build; do not hand-edit build output.
Copy the source assets when reusing the identity so a site never depends on a
generation-session or external image URL.
