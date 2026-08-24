# Package distribution — design

Date: 2026-08-24
Status: approved (design), pending implementation plan

## Goal

Let users install `dwshell` through the native channels of the major platforms,
served from the project's own infrastructure:

- **Homebrew** (macOS + Linux) via a tap.
- **APT** (Debian/Ubuntu), **YUM/DNF** (Fedora/RHEL/CentOS), **APK** (Alpine)
  via signed repositories hosted on GitHub Pages.

Reference: `ale-rinaldi/brrm` builds a single flat APT repo on Pages. This design
generalizes that to four channels with broader coverage, driven by the existing
tag-triggered release.

## Approved decisions

- **Hosting**: GitHub Pages on the main repo `porech/dwshell`. All package data
  lives under `/dist/{apt,yum,apk}`; the site root stays free (minimal
  placeholder) for a future landing/docs page.
- **Coverage**: APT + YUM + APK + Homebrew.
- **Homebrew tap**: `porech/homebrew-tap` → `brew install porech/tap/dwshell`.
- **Signing**: a dedicated GPG key for APT/YUM (and RPM package signing); a
  separate dedicated RSA key for the APK index (Alpine does not use GPG).
- **Versioning**: **latest-only** — the repos are rebuilt from the current
  release on every tag. Older versions remain downloadable from GitHub Releases.
- **Key backups**: private + public keys are copied to `~/dwshell-keys/` (outside
  the repo, `0600`) as the user's backup. A `.gitignore` guards key patterns.

## Architecture

Everything hangs off the existing tag-triggered `release.yml`:

```
release.yml  (on: push tag v*)
├── test        reusable ci.yml — must pass (already wired)
├── goreleaser  needs: test
│     ├── binaries + archives + checksums              (existing)
│     ├── nfpms → .deb / .rpm / .apk  (amd64, arm64)   (new)
│     ├── brews → Formula/dwshell.rb on porech/homebrew-tap (new)
│     └── GitHub Release with all artifacts
└── pages       needs: goreleaser
      ├── download the built packages (goreleaser dist artifact)
      ├── import GPG + RSA signing keys from secrets
      ├── build dist/apt, dist/yum, dist/apk (indexes + signatures + pubkeys)
      ├── render dist/index.html (install instructions) + minimal root index
      └── deploy to GitHub Pages (actions/upload-pages-artifact + deploy-pages)
```

### Packaging — goreleaser `nfpms`

The binary is static (`CGO_ENABLED=0`), so packaging is trivial. One `nfpms`
block emits `.deb`, `.rpm`, `.apk` for amd64 + arm64:

- `/usr/bin/dwshell`
- `LICENSE`, `README.md` → `/usr/share/doc/dwshell/`
- metadata: maintainer, description, MIT, homepage, section `utils`
- RPMs are GPG-signed (`nfpms.rpm.signature.key_file`) so `dnf` can run
  `gpgcheck=1` on packages. `.deb` needs no package-level signature (APT trusts
  the repo `Release` signature).

### Homebrew — goreleaser `brews`

goreleaser updates `Formula/dwshell.rb` in `porech/homebrew-tap` on each release,
using the darwin+linux `tar.gz` archives already produced (with their SHA256).
Formula: homepage, MIT, `bin.install "dwshell"`, a `test do` running
`dwshell version`. Auth via secret `HOMEBREW_TAP_GITHUB_TOKEN` (the default
`GITHUB_TOKEN` cannot push to a second repo).

### Repositories on Pages

- **APT** (`/dist/apt`, flat repo): `dpkg-scanpackages` + `apt-ftparchive`;
  sign `InRelease` (clearsigned) + `Release.gpg` (detached); publish `key.asc`.
  ```sh
  sudo install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://porech.github.io/dwshell/dist/apt/key.asc \
    | sudo tee /etc/apt/keyrings/dwshell.asc >/dev/null
  echo "deb [signed-by=/etc/apt/keyrings/dwshell.asc] https://porech.github.io/dwshell/dist/apt ./" \
    | sudo tee /etc/apt/sources.list.d/dwshell.list
  sudo apt update && sudo apt install dwshell
  ```
- **YUM** (`/dist/yum`): `createrepo_c`; GPG-sign `repodata/repomd.xml` →
  `repomd.xml.asc`; publish `key.asc` and a `dwshell.repo` with
  `repo_gpgcheck=1` + `gpgcheck=1`.
  ```sh
  sudo curl -fsSL https://porech.github.io/dwshell/dist/yum/dwshell.repo \
    -o /etc/yum.repos.d/dwshell.repo
  sudo dnf install dwshell
  ```
- **APK** (`/dist/apk`): built in an Alpine container — `apk index` →
  `APKINDEX.tar.gz`, signed with `abuild-sign` using the RSA key; publish the
  `.rsa.pub`. Users drop the pubkey into `/etc/apk/keys/` and add the repo URL.

### Keys & secrets (bootstrapped autonomously via `gh`)

`gh` is authenticated as org `porech` admin with `repo` + `delete_repo` scope, so
the setup is done from CI/CLI with no manual GitHub UI steps:

| Secret | Repo | Use |
|---|---|---|
| `GPG_PRIVATE_KEY` | dwshell | sign APT/YUM metadata + RPM packages (passphraseless, CI-only) |
| `APK_RSA_PRIVATE_KEY` | dwshell | sign the APK index |
| `HOMEBREW_TAP_GITHUB_TOKEN` | dwshell | push the formula to `porech/homebrew-tap` |

Autonomous setup steps:
1. Generate a dedicated GPG key (passwordless) and an RSA keypair for APK; save
   private+public copies to `~/dwshell-keys/` (0600).
2. `gh repo create porech/homebrew-tap --public` (seed with a README).
3. `gh secret set` the three secrets on `porech/dwshell`.
4. Enable Pages with `build_type: workflow` via the REST API.

`HOMEBREW_TAP_GITHUB_TOKEN` is bootstrapped from the current `gh` token (scope
`repo`). **Recommendation (documented, non-blocking):** replace it later with a
dedicated fine-grained PAT (contents:write on `porech/homebrew-tap` only) or a
GitHub App, so CI does not depend on a personal token's lifetime.

## Security / legal

- No private key ever enters the repo; a `.gitignore` blocks `*.asc`, `*.gpg`,
  `*.rsa`, `*.key`, `*.pem` patterns as a backstop.
- Pages hosts only install instructions and public keys.
- The site root stays a minimal placeholder; nothing sensitive is served.
- Signing keys are dedicated to dwshell and revocable without collateral impact.

## Validation plan

- `goreleaser release --snapshot --clean` locally to confirm nfpms + brews config
  produce all artifacts (deb/rpm/apk for both arches, formula file).
- Lint packages: `dpkg-deb -I`, `rpm -qip`, `apk` metadata inspection.
- Dry-run the Pages assembly script locally against the snapshot artifacts;
  verify `apt-ftparchive`/`createrepo_c`/`apk index` output and GPG/RSA
  signatures validate against the public keys.
- After the first real tag: install end-to-end in throwaway containers
  (debian, fedora, alpine) and via `brew install` on macOS.

## Out of scope (for now)

- Arch/AUR (declined).
- Keeping historical versions in the repos (latest-only chosen).
- Windows package managers (winget/scoop/choco) — possible future channel.
