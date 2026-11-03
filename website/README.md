# Website

Static HTML/CSS with no build dependencies. Product diagrams are labeled concepts, not screenshots.

The GitHub Actions workflow uses `workflow_dispatch` only. It uploads `website/` when a maintainer explicitly runs it; a source push does not deploy the site.

The repository Pages base is `https://sogud.github.io/Muse/`. Assets use relative paths. The self-contained 404 links to the first path segment on github.io and `/` on a custom domain. A filesystem preview does not emulate hosting 404 routing.

The website contains no analytics, forms, account system or third-party scripts. Deployment, custom domains and binary releases are separate operations.
