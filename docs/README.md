# Parallax Documentation

Technical documentation for the Parallax protocol and CLI client. Published at [docs.parallaxprotocol.org](https://docs.parallaxprotocol.org).

## Local development

Install the [Mintlify CLI](https://www.npmjs.com/package/mint):

```
npm i -g mint
```

Preview locally:

```
mint dev
```

View at `http://localhost:3000`.

## Versions

The site is versioned **by minor series**, not by individual release. Patch
releases within a series rarely change the documentation, so one folder covers
all of them — `v1.2.x/` serves both v1.2.0 and v1.2.1, and a future v2.0.1 needs
no new folder. `docs.json` declares the series under `navigation.versions`:

| Folder    | Version picker         | Notes                                              |
| --------- | ---------------------- | -------------------------------------------------- |
| `v2.1.x/` | hidden                 | In development. Reachable at `/v2.1.x/…`, but absent from the picker, search engines and sitemaps until release. |
| `v2.0.x/` | `v2.0.x` — **default** | Current public release. Served at `/v2.0.x/…`.      |
| `v1.2.x/` | `v1.2.x`               | The v1 client (`prlx`), imported from the archived [ParallaxProtocol/parallax-docs](https://github.com/ParallaxProtocol/parallax-docs) repository. Documents features dropped in v2.0.0, notably light-client sync and the `les` RPC namespace. Frozen; no longer maintained. |
| `v1.1.x/` | `v1.1.x`               | Same content as `v1.2.x/`, copied. The v1.1 releases shipped against the same docs, but each series still needs its own folder — see below. Frozen. |

What a major, minor, or patch release of the client itself means — and why a
version bump never implies a consensus change — is documented on the
[Versioning](v2.1.x/parallax-client/fundamentals/versioning.mdx) page.

Every released tag redirects to the series that documents it, so
`/v1.2.1/…` and `/v2.0.0/…` resolve for readers who know their exact version.
Add a line to that redirect block when a new patch release ships.

Content is duplicated per series on purpose — Mintlify page paths are literal
file paths, so a series folder is a frozen snapshot of the docs as that line
shipped. **Edit the folder for the release you are documenting.** Work on
unreleased behaviour belongs in `v2.1.x/` only; a fix that also applies to the
shipped release goes in both.

Assets are shared and stay unversioned: `index.mdx` (landing page), `logo/`,
`images/`, `favicon.svg`.

### Links inside pages

Always use **relative** links, and always write the leading `./` or `../`:
`./sibling`, `../other-section/page`.

- An absolute link like `/parallax-client/fundamentals/cli` escapes the version
  folder and silently sends readers to whichever version is currently default.
- A bare link like `(peer-management)` is **not** treated as a sibling. Mintlify
  resolves it from the site root, so it breaks. Write `(./peer-management)`.

Run `mint broken-links` before pushing; it catches both cases.

### Releasing

**A patch release** (`v2.0.1`) needs no new folder. Add its tag to the redirect
block so `/v2.0.1/…` resolves, and edit `v2.0.x/` if the docs changed.

**A new minor series** (`v2.2.0`) needs one:

1. Start the folder from the outgoing development series — `cp -r docs/v2.1.x
   docs/v2.2.x` — or snapshot a tag with
   `git archive vX.Y.Z docs/guides docs/parallax-client docs/parallax-protocol | tar -x --strip-components=1 -C docs/vX.Y.x`.
2. In `docs.json`, copy the series' `tabs` block and rewrite the path prefix to
   `vX.Y.x/`.
3. Move `default: true` and `tag: "latest"` onto the newly released series, and
   drop `hidden` from it. Add the next development series as the new `hidden`
   entry.
4. Repoint the fall-through redirects at the bottom of `docs.json`
   (`/guides/:slug*`, `/parallax-client/:slug*`, `/parallax-protocol/:slug*`)
   to the new default, and add the release tag to the tag redirect block.

### Every version needs its own folder — no aliases

**A page path must belong to exactly one version.** Mintlify works out which
version the reader is on by matching the URL against each version's page list.
If two versions claim the same path, that lookup is ambiguous and the picker
falls back to the default version.

So when a release ships against docs that already exist, copy the tree into a
folder of its own rather than pointing the new version at the old one's pages.
`v1.1.x/` is a byte-for-byte copy of `v1.2.x/` for this reason.

Two shortcuts look reasonable and both fail — the JSON schema accepts each, and
`mint broken-links` reports no problem either way:

| Shortcut | What happens |
| -------- | ------------ |
| `{"version": "v1.1.x", "href": "/v1.2.x/…"}` | The picker builds entries from a version's own pages, so an entry with no `pages` has nothing behind it. It renders, but clicking does nothing. |
| `{"version": "v1.1.x", "pages": ["v1.2.x/…"]}` | Now two versions claim that path. Clicking **v1.2.x** lands on the page with the picker still reading the default version and the wrong sidebar — it breaks the version it was aliasing. |

Verify with `mint dev` and actually switch versions in the dropdown. Neither
failure shows up in `mint broken-links` or in schema validation.

### Redirects

Pre-versioning URLs (`/parallax-client/fundamentals/cli`) fall through to the
current default version. Those rules are deliberately **not** permanent, since
their target moves each time a new release becomes the default. Permanent
redirects are reserved for mappings that will never move, such as genuine page
renames.

## Structure

Within each version folder:

- `guides/` — User guides for wallets, mining, and client setup
- `parallax-client/` — CLI client documentation (configuration, RPC, tracing, tools)
- `parallax-protocol/` — Protocol documentation (consensus, PVM, networking, data structures)

## Publishing

Changes pushed to the default branch are deployed automatically via the Mintlify GitHub integration.
