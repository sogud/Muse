# Website preview

Static HTML/CSS with no build dependencies. Product diagrams are labeled concepts, not screenshots.

## Deployment

The GitHub Actions workflow is manual (`workflow_dispatch`) only. It uploads `website/` after a maintainer explicitly requests deployment. No deployment has been run.

Expected default Pages base: `https://sogud.github.io/Muse/`. Planned custom domain: `muse.sogud.me` (not configured). No CNAME file or DNS changes. Repository/Pages availability must be confirmed before publication.

Home-page assets use relative paths. The self-contained 404 needs no assets: its home link uses the first path segment on github.io and `/` on a custom domain. A plain filesystem preview does not emulate hosting 404 routing.

## Acceptance

Static structure, whitespace, public link availability and workflow YAML checked. Browser rendering was checked at 1440px and 390px on 2026-11-01. Hosting behavior and social preview image remain unverified. No analytics, forms, account system or third-party scripts are included.

2026-11-01 local revision: reduced the homepage to product purpose, a labeled visual or command example, and current status. No release or deployment was performed.
Current native UI scope was checked against AGENTS.md and native_ui.go: viewing projects/jobs/artifacts and managing configuration, without GUI creative-request input.
