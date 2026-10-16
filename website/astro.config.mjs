import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";
import starlightLinksValidator from "starlight-links-validator";

export default defineConfig({
  site: "https://khoist.devantler.tech",
  integrations: [
    starlight({
      title: "KHoist",
      description:
        "Publish, discover and compose data products through standard APIs.",
      favicon: "/brand/khoist-mark.svg",
      logo: {
        src: "./public/brand/khoist-mark.svg",
        replacesTitle: false,
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/devantler-tech/data-product-controller",
        },
      ],
      editLink: {
        baseUrl:
          "https://github.com/devantler-tech/data-product-controller/edit/main/website/",
      },
      head: [
        {
          tag: "link",
          attrs: { rel: "preconnect", href: "https://fonts.googleapis.com" },
        },
        {
          tag: "link",
          attrs: {
            rel: "preconnect",
            href: "https://fonts.gstatic.com",
            crossorigin: true,
          },
        },
        {
          tag: "link",
          attrs: {
            rel: "stylesheet",
            href: "https://fonts.googleapis.com/css2?family=Bricolage+Grotesque:opsz,wght@12..96,600;700&family=Hanken+Grotesque:wght@400;500;600;700&display=swap",
          },
        },
      ],
      customCss: ["./src/styles/custom.css"],
      plugins: [starlightLinksValidator()],
      sidebar: [
        {
          label: "Start here",
          items: [
            { label: "Overview", link: "/" },
            { label: "Try a data product", link: "/start/quickstart/" },
            { label: "Install the controller", link: "/start/installation/" },
          ],
        },
        {
          label: "Guides",
          items: [
            { label: "Publish an API", link: "/guides/publish-an-api/" },
            {
              label: "Check declarations",
              link: "/guides/check-declarations/",
            },
            { label: "Consume a product", link: "/guides/consume-a-product/" },
            { label: "Compose products", link: "/guides/compose-products/" },
            { label: "Upgrade an installation", link: "/guides/upgrade/" },
          ],
        },
        {
          label: "Understand KHoist",
          items: [
            {
              label: "Architecture and ownership",
              link: "/concepts/architecture/",
            },
            {
              label: "Capabilities and maturity",
              link: "/concepts/capabilities/",
            },
          ],
        },
        {
          label: "Reference",
          items: [
            { label: "DataProduct", link: "/reference/dataproduct/" },
            { label: "Registry HTTP API", link: "/reference/registry-api/" },
            { label: "Configuration", link: "/reference/configuration/" },
            { label: "Troubleshooting", link: "/troubleshooting/" },
          ],
        },
      ],
      tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 3 },
      pagination: true,
    }),
  ],
});
