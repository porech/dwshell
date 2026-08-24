# Package Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Distribute `dwshell` via Homebrew and signed APT/YUM/APK repositories hosted on GitHub Pages, all driven by the existing tag-triggered release.

**Architecture:** goreleaser gains `nfpms` (deb/rpm/apk) and `brews` (tap formula) blocks. On a tag, `release.yml` runs tests → goreleaser (packages + formula + GitHub Release) → a new `pages` job that downloads the packages, signs and assembles APT/YUM/APK repos under `dist/`, renders install pages, and deploys to Pages. Signing is done entirely in the pages job (GPG for APT/YUM + RPM packages; a dedicated RSA key for the APK index), keeping goreleaser free of secrets.

**Tech Stack:** Go, goreleaser v2, GitHub Actions, GitHub Pages, `dpkg-dev`/`apt-utils`, `createrepo_c`, `rpm`, Alpine `abuild`/`apk`, GnuPG.

**Spec:** `docs/superpowers/specs/2026-08-24-package-distribution-design.md`

## Global Constraints

- Module/owner: `github.com/porech/dwshell`; org `porech`; Pages URL base `https://porech.github.io/dwshell`.
- Package repos live under `dist/{apt,yum,apk}`; site root stays a minimal placeholder.
- **Latest-only**: repos are rebuilt from the current release each tag; no version history in the repos.
- Binary is static (`CGO_ENABLED=0`), amd64 + arm64, license MIT.
- Homebrew tap repo: `porech/homebrew-tap`; install form `brew install porech/tap/dwshell`.
- Signing keys are dedicated to dwshell: GPG (APT/YUM/RPM), RSA (APK). Private keys NEVER enter the repo; local backups in `~/dwshell-keys/` (0600).
- Secrets on `porech/dwshell`: `GPG_PRIVATE_KEY`, `APK_RSA_PRIVATE_KEY`, `HOMEBREW_TAP_GITHUB_TOKEN`.
- Package maintainer metadata (public): `Alessandro Rinaldi <ale-rinaldi@users.noreply.github.com>`.
- goreleaser v2 key names: `brews[].repository`, `brews[].directory` (not the old `tap`/`folder`).

---

### Task 1: Guard against committing key material

**Files:**
- Modify: `.gitignore`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing (safety net for later tasks that generate keys).

- [ ] **Step 1: Append key patterns to `.gitignore`**

```gitignore

# Signing key material — must never be committed (see docs/.../package-distribution-design.md)
*.asc
*.gpg
*.rsa
*.rsa.pub
*.key
*.pem
dwshell-keys/
```

- [ ] **Step 2: Verify the patterns take effect**

Run:
```bash
touch test.asc test.rsa && git check-ignore test.asc test.rsa; rm -f test.asc test.rsa
```
Expected: prints `test.asc` and `test.rsa` (both ignored).

- [ ] **Step 3: Commit**

```bash
git add .gitignore
git commit -m "chore: gitignore signing key material"
```

---

### Task 2: Build native packages with goreleaser (nfpms)

**Files:**
- Modify: `.goreleaser.yaml`

**Interfaces:**
- Consumes: existing `builds[0].id: dwshell`.
- Produces: `.deb`/`.rpm`/`.apk` artifacts in `dist/` named `dwshell_<version>_<os>_<arch>.<ext>`, consumed by the APT/YUM/APK build scripts (Tasks 6–8) and the pages job (Task 10).

- [ ] **Step 1: Add the `nfpms` block** after the `archives:` block in `.goreleaser.yaml`

```yaml
nfpms:
  - id: packages
    package_name: dwshell
    ids:
      - dwshell
    vendor: porech
    homepage: https://github.com/porech/dwshell
    maintainer: Alessandro Rinaldi <ale-rinaldi@users.noreply.github.com>
    description: |-
      SSH-style remote shell and file transfer over DWService.
      Opens a shell on remote DWService agents and moves files to/from them.
    license: MIT
    formats:
      - deb
      - rpm
      - apk
    bindir: /usr/bin
    section: utils
    contents:
      - src: LICENSE
        dst: /usr/share/doc/dwshell/LICENSE
      - src: README.md
        dst: /usr/share/doc/dwshell/README.md
```

- [ ] **Step 2: Validate the config parses**

Run: `goreleaser check`
Expected: `1 configuration file(s) validated` with no errors.

- [ ] **Step 3: Build a snapshot and confirm all packages are produced**

Run: `goreleaser release --snapshot --clean`
Then: `ls dist/*.deb dist/*.rpm dist/*.apk`
Expected: a `.deb`, `.rpm`, and `.apk` for both `amd64` and `arm64` (6 files).

- [ ] **Step 4: Inspect one package's metadata**

Run: `dpkg-deb -I dist/dwshell_*_linux_amd64.deb` (or `dpkg-deb --contents`)
Expected: control shows `Package: dwshell`, the MIT/description fields, and the binary mapped to `/usr/bin/dwshell`.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml
git commit -m "release: build deb/rpm/apk packages via nfpms"
```

---

### Task 3: Publish the Homebrew formula (brews)

**Files:**
- Modify: `.goreleaser.yaml`

**Interfaces:**
- Consumes: existing `archives[0]` (darwin + linux tar.gz).
- Produces: `Formula/dwshell.rb` pushed to `porech/homebrew-tap` on real releases (skipped on snapshot). Consumes secret `HOMEBREW_TAP_GITHUB_TOKEN` at release time (Task 5 sets it).

- [ ] **Step 1: Add the `brews` block** after `nfpms:` in `.goreleaser.yaml`

```yaml
brews:
  - name: dwshell
    ids:
      - default
    repository:
      owner: porech
      name: homebrew-tap
      token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"
    directory: Formula
    homepage: https://github.com/porech/dwshell
    description: SSH-style remote shell and file transfer over DWService
    license: MIT
    commit_author:
      name: goreleaser-bot
      email: noreply@github.com
    install: |
      bin.install "dwshell"
    test: |
      system "#{bin}/dwshell", "version"
```

- [ ] **Step 2: Validate the config parses**

Run: `goreleaser check`
Expected: validated, no errors. (The formula is only pushed on a real tag release, not on snapshot — that path is exercised end-to-end in Task 12.)

- [ ] **Step 3: Commit**

```bash
git add .goreleaser.yaml
git commit -m "release: publish Homebrew formula to porech/homebrew-tap"
```

---

### Task 4: Generate signing keys and back them up

**Files:**
- Create (outside repo): `~/dwshell-keys/dwshell-gpg-private.asc`, `~/dwshell-keys/dwshell-gpg-public.asc`, `~/dwshell-keys/dwshell-apk.rsa`, `~/dwshell-keys/dwshell-apk.rsa.pub`

**Interfaces:**
- Consumes: nothing.
- Produces: the GPG key (uid `dwshell package signing <ale-rinaldi@users.noreply.github.com>`) and RSA keypair used by Tasks 5–8. Note the GPG key's long ID/fingerprint for later steps.

- [ ] **Step 1: Create the backup directory (0700)**

```bash
mkdir -p ~/dwshell-keys && chmod 700 ~/dwshell-keys
```

- [ ] **Step 2: Generate a passwordless GPG key in a throwaway keyring**

```bash
export GNUPGHOME="$(mktemp -d)"
cat > "$GNUPGHOME/keyparams" <<'EOF'
%no-protection
Key-Type: eddsa
Key-Curve: ed25519
Subkey-Type: ecdh
Subkey-Curve: cv25519
Name-Real: dwshell package signing
Name-Email: ale-rinaldi@users.noreply.github.com
Expire-Date: 0
%commit
EOF
gpg --batch --generate-key "$GNUPGHOME/keyparams"
GPG_FPR=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')
echo "GPG fingerprint: $GPG_FPR"
```
Expected: prints a 40-char fingerprint.

- [ ] **Step 3: Export the GPG key pair to the backup dir**

```bash
gpg --armor --export-secret-keys "$GPG_FPR" > ~/dwshell-keys/dwshell-gpg-private.asc
gpg --armor --export "$GPG_FPR"             > ~/dwshell-keys/dwshell-gpg-public.asc
chmod 600 ~/dwshell-keys/dwshell-gpg-*.asc
```

- [ ] **Step 4: Generate the APK RSA keypair to the backup dir**

```bash
openssl genrsa -out ~/dwshell-keys/dwshell-apk.rsa 4096
openssl rsa -in ~/dwshell-keys/dwshell-apk.rsa -pubout -out ~/dwshell-keys/dwshell-apk.rsa.pub
chmod 600 ~/dwshell-keys/dwshell-apk.rsa
```

- [ ] **Step 5: Verify all four backup files exist and are non-empty**

Run: `ls -l ~/dwshell-keys/`
Expected: four files, private keys `-rw-------`.

- [ ] **Step 6: No commit** (keys must never enter the repo). Record `GPG_FPR` for Tasks 5, 7.

---

### Task 5: Bootstrap GitHub infrastructure (tap, secrets, Pages)

**Files:** none (uses `gh`; `gh` is authed as org `porech` admin with `repo`+`delete_repo`).

**Interfaces:**
- Consumes: keys from Task 4.
- Produces: repo `porech/homebrew-tap`; secrets `GPG_PRIVATE_KEY`, `APK_RSA_PRIVATE_KEY`, `HOMEBREW_TAP_GITHUB_TOKEN` on `porech/dwshell`; Pages enabled with `build_type: workflow`.

- [ ] **Step 1: Create the Homebrew tap repo (seeded so it is clonable)**

```bash
gh repo create porech/homebrew-tap --public \
  --description "Homebrew tap for porech tools" \
  --add-readme
```
Expected: repo created. Verify: `gh repo view porech/homebrew-tap --json url -q .url`.

- [ ] **Step 2: Set the signing-key secrets on `porech/dwshell`**

```bash
gh secret set GPG_PRIVATE_KEY     -R porech/dwshell < ~/dwshell-keys/dwshell-gpg-private.asc
gh secret set APK_RSA_PRIVATE_KEY -R porech/dwshell < ~/dwshell-keys/dwshell-apk.rsa
```

- [ ] **Step 3: Set the Homebrew push token secret**

```bash
gh auth token | gh secret set HOMEBREW_TAP_GITHUB_TOKEN -R porech/dwshell
```
Note (documented in README, non-blocking): replace later with a dedicated fine-grained PAT (contents:write on `porech/homebrew-tap` only) so CI does not depend on a personal token.

- [ ] **Step 4: Enable Pages with GitHub Actions as the build source**

```bash
gh api -X POST repos/porech/dwshell/pages -f build_type=workflow || \
gh api -X PUT  repos/porech/dwshell/pages -f build_type=workflow
```
Expected: HTTP 201/204. Verify: `gh api repos/porech/dwshell/pages -q .build_type` → `workflow`.

- [ ] **Step 5: Confirm secrets are present**

Run: `gh secret list -R porech/dwshell`
Expected: the three secret names listed.

- [ ] **Step 6: No commit.**

---

### Task 6: APT repository build script

**Files:**
- Create: `scripts/pages/build-apt.sh`

**Interfaces:**
- Consumes: env `GPG_FPR` (signing key in the active keyring), a directory of `.deb` files.
- Produces: `dist-site/dist/apt/` with `Packages(.gz)`, signed `Release`/`InRelease`/`Release.gpg`, `key.asc`. Invoked by Task 10 as `build-apt.sh <deb_dir> <out_dir>`.

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# Build a signed flat APT repository from a directory of .deb files.
# Usage: build-apt.sh <deb_dir> <out_dir>   (requires GPG_FPR + gnupg, dpkg-dev, apt-utils)
set -euo pipefail
deb_dir="${1:?deb dir}"; out="${2:?out dir}"
apt="$out/dist/apt"
rm -rf "$apt"; mkdir -p "$apt"
cp "$deb_dir"/*.deb "$apt/"
cd "$apt"
dpkg-scanpackages --multiversion . > Packages
gzip -9 -k -f Packages
apt-ftparchive \
  -o APT::FTPArchive::Release::Origin="porech/dwshell" \
  -o APT::FTPArchive::Release::Label="dwshell" \
  -o APT::FTPArchive::Release::Suite="stable" \
  -o APT::FTPArchive::Release::Codename="dwshell" \
  -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
  -o APT::FTPArchive::Release::Components="main" \
  -o APT::FTPArchive::Release::Description="dwshell APT repository" \
  release . > Release
gpg --batch --yes --armor --detach-sign --default-key "$GPG_FPR" -o Release.gpg Release
gpg --batch --yes --clearsign          --default-key "$GPG_FPR" -o InRelease  Release
gpg --armor --export "$GPG_FPR" > key.asc
echo "APT repo built at $apt"
```

- [ ] **Step 2: Make it executable**

```bash
chmod +x scripts/pages/build-apt.sh
```

- [ ] **Step 3: Run it against the snapshot debs (needs Task 4's keyring + `GPG_FPR`)**

```bash
mkdir -p /tmp/debs && cp dist/*.deb /tmp/debs/
GPG_FPR="$GPG_FPR" scripts/pages/build-apt.sh /tmp/debs /tmp/site
```
Expected: `APT repo built at /tmp/site/dist/apt`.

- [ ] **Step 4: Verify the Release signature validates**

```bash
gpg --verify /tmp/site/dist/apt/Release.gpg /tmp/site/dist/apt/Release && \
grep -q "Package: dwshell" /tmp/site/dist/apt/Packages && echo OK
```
Expected: `Good signature` and `OK`.

- [ ] **Step 5: Commit**

```bash
git add scripts/pages/build-apt.sh
git commit -m "pages: APT repository build script"
```

---

### Task 7: YUM repository build script (with signed RPMs)

**Files:**
- Create: `scripts/pages/build-yum.sh`

**Interfaces:**
- Consumes: env `GPG_FPR`, a directory of `.rpm` files.
- Produces: `dist-site/dist/yum/` with signed RPMs, `repodata/` (signed `repomd.xml.asc`), `key.asc`, and `dwshell.repo`. Invoked as `build-yum.sh <rpm_dir> <out_dir>`.

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# Build a signed YUM/DNF repository from a directory of .rpm files.
# Usage: build-yum.sh <rpm_dir> <out_dir>   (requires GPG_FPR, rpm, createrepo_c, gnupg)
set -euo pipefail
rpm_dir="${1:?rpm dir}"; out="${2:?out dir}"
yum="$out/dist/yum"
rm -rf "$yum"; mkdir -p "$yum"
cp "$rpm_dir"/*.rpm "$yum/"

# Sign the RPM packages so dnf can enforce gpgcheck=1.
GPG_NAME="$(gpg --list-keys --with-colons "$GPG_FPR" | awk -F: '/^uid:/ {print $10; exit}')"
cat > "$HOME/.rpmmacros" <<EOF
%_signature gpg
%_gpg_name $GPG_FPR
%__gpg_sign_cmd %{__gpg} gpg --batch --no-armor --pinentry-mode loopback --no-secmem-warning -u "%{_gpg_name}" --detach-sign -o %{__signature_filename} %{__plaintext_filename}
EOF
rpm --addsign "$yum"/*.rpm

createrepo_c "$yum"
gpg --batch --yes --armor --detach-sign --default-key "$GPG_FPR" \
    -o "$yum/repodata/repomd.xml.asc" "$yum/repodata/repomd.xml"
gpg --armor --export "$GPG_FPR" > "$yum/key.asc"

cat > "$yum/dwshell.repo" <<'EOF'
[dwshell]
name=dwshell
baseurl=https://porech.github.io/dwshell/dist/yum
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://porech.github.io/dwshell/dist/yum/key.asc
EOF
echo "YUM repo built at $yum"
```

- [ ] **Step 2: Make it executable**

```bash
chmod +x scripts/pages/build-yum.sh
```

- [ ] **Step 3: Install tooling if missing, then run against snapshot rpms**

```bash
# On the CI runner (ubuntu) these come from apt; locally on macOS use the CI to validate.
mkdir -p /tmp/rpms && cp dist/*.rpm /tmp/rpms/
GPG_FPR="$GPG_FPR" scripts/pages/build-yum.sh /tmp/rpms /tmp/site
```
Expected: `YUM repo built at /tmp/site/dist/yum`.

- [ ] **Step 4: Verify repomd signature and package signature**

```bash
gpg --verify /tmp/site/dist/yum/repodata/repomd.xml.asc /tmp/site/dist/yum/repodata/repomd.xml && \
rpm --checksig /tmp/site/dist/yum/dwshell_*_linux_amd64.rpm | grep -qi "signatures OK\|pgp" && echo OK
```
Expected: `Good signature` and `OK`.

- [ ] **Step 5: Commit**

```bash
git add scripts/pages/build-yum.sh
git commit -m "pages: YUM repository build script with signed RPMs"
```

---

### Task 8: APK repository build script (Alpine)

**Files:**
- Create: `scripts/pages/build-apk.sh`

**Interfaces:**
- Consumes: an RSA private key path, a directory of `.apk` files.
- Produces: `dist-site/dist/apk/<arch>/APKINDEX.tar.gz` (signed) + the `.rsa.pub`. Runs inside an Alpine container. Invoked as `build-apk.sh <apk_dir> <out_dir> <rsa_key>`.

- [ ] **Step 1: Write the script (runs inside the Alpine container)**

```bash
#!/usr/bin/env sh
# Build a signed APK repository. Run inside alpine with abuild installed.
# Usage: build-apk.sh <apk_dir> <out_dir> <rsa_key_path>
set -eu
apk_dir="$1"; out="$2"; key="$3"
base="$out/dist/apk"
rm -rf "$base"; mkdir -p "$base"
# goreleaser arch -> alpine arch
map_arch() { case "$1" in *amd64*) echo x86_64 ;; *arm64*) echo aarch64 ;; *) echo "" ;; esac; }
for f in "$apk_dir"/*.apk; do
  a="$(map_arch "$f")"; [ -n "$a" ] || continue
  mkdir -p "$base/$a"; cp "$f" "$base/$a/"
done
pub="dwshell-$(basename "$key").pub"
cp "$key.pub" "$base/$pub" 2>/dev/null || cp "${key}.pub" "$base/$pub"
for d in "$base"/*/; do
  [ -d "$d" ] || continue
  ( cd "$d" && apk index -o APKINDEX.tar.gz ./*.apk && abuild-sign -k "$key" APKINDEX.tar.gz )
done
echo "APK repo built at $base"
```

- [ ] **Step 2: Make it executable**

```bash
chmod +x scripts/pages/build-apk.sh
```

- [ ] **Step 3: Run it inside an Alpine container against snapshot apks**

```bash
mkdir -p /tmp/apks && cp dist/*.apk /tmp/apks/
docker run --rm \
  -v /tmp/apks:/apks -v /tmp/site:/site \
  -v "$HOME/dwshell-keys/dwshell-apk.rsa:/key.rsa" \
  -v "$HOME/dwshell-keys/dwshell-apk.rsa.pub:/key.rsa.pub" \
  -v "$PWD/scripts/pages/build-apk.sh:/build-apk.sh" \
  alpine:latest sh -c "apk add --no-cache abuild >/dev/null && sh /build-apk.sh /apks /site /key.rsa"
```
Expected: `APK repo built at /site/dist/apk` and `ls /tmp/site/dist/apk/x86_64/APKINDEX.tar.gz` exists.

- [ ] **Step 4: Verify the index is signed (contains a .SIGN entry)**

```bash
tar tzf /tmp/site/dist/apk/x86_64/APKINDEX.tar.gz | grep -q '^.SIGN' && echo OK
```
Expected: `OK`.

- [ ] **Step 5: Commit**

```bash
git add scripts/pages/build-apk.sh
git commit -m "pages: APK repository build script"
```

---

### Task 9: Landing pages generator

**Files:**
- Create: `scripts/pages/render-index.sh`

**Interfaces:**
- Consumes: the assembled `dist-site/dist/` tree.
- Produces: `dist-site/dist/index.html` (install instructions for all channels) and `dist-site/index.html` (minimal root placeholder). Invoked as `render-index.sh <out_dir>`.

- [ ] **Step 1: Write the generator**

```bash
#!/usr/bin/env bash
# Render the install landing page (dist/index.html) and a minimal root placeholder.
# Usage: render-index.sh <out_dir>
set -euo pipefail
out="${1:?out dir}"
mkdir -p "$out/dist"

cat > "$out/index.html" <<'EOF'
<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>dwshell</title></head>
<body style="font-family:system-ui,sans-serif;max-width:640px;margin:4em auto;padding:0 1em;line-height:1.5">
<h1>dwshell</h1>
<p>SSH-style remote shell and file transfer over DWService.</p>
<p><a href="dist/">Installation &amp; package repositories →</a> ·
   <a href="https://github.com/porech/dwshell">GitHub</a></p>
</body></html>
EOF

cat > "$out/dist/index.html" <<'EOF'
<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Install dwshell</title>
<style>
 body{font-family:system-ui,sans-serif;max-width:820px;margin:2em auto;padding:0 1em;line-height:1.55}
 code,pre{background:#f4f4f4;border-radius:4px} code{padding:.15em .4em}
 pre{padding:1em;overflow-x:auto} h2{margin-top:1.6em}
</style></head>
<body>
<h1>Install dwshell</h1>

<h2>Homebrew (macOS &amp; Linux)</h2>
<pre><code>brew install porech/tap/dwshell</code></pre>

<h2>Debian / Ubuntu (APT)</h2>
<pre><code>sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://porech.github.io/dwshell/dist/apt/key.asc | sudo tee /etc/apt/keyrings/dwshell.asc >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/dwshell.asc] https://porech.github.io/dwshell/dist/apt ./" | sudo tee /etc/apt/sources.list.d/dwshell.list
sudo apt update &amp;&amp; sudo apt install dwshell</code></pre>

<h2>Fedora / RHEL / CentOS (DNF)</h2>
<pre><code>sudo curl -fsSL https://porech.github.io/dwshell/dist/yum/dwshell.repo -o /etc/yum.repos.d/dwshell.repo
sudo dnf install dwshell</code></pre>

<h2>Alpine (APK)</h2>
<pre><code>sudo curl -fsSL https://porech.github.io/dwshell/dist/apk/dwshell-dwshell-apk.rsa.pub -o /etc/apk/keys/dwshell.rsa.pub
echo "https://porech.github.io/dwshell/dist/apk" | sudo tee -a /etc/apk/repositories
sudo apk update &amp;&amp; sudo apk add dwshell</code></pre>

<h2>Direct download</h2>
<p>Pre-built binaries for every platform are attached to each
<a href="https://github.com/porech/dwshell/releases">GitHub release</a>.</p>
</body></html>
EOF
echo "index pages rendered under $out"
```

- [ ] **Step 2: Make it executable and run it**

```bash
chmod +x scripts/pages/render-index.sh
scripts/pages/render-index.sh /tmp/site
```
Expected: `index pages rendered under /tmp/site`; both `/tmp/site/index.html` and `/tmp/site/dist/index.html` exist.

- [ ] **Step 3: Sanity-check the APK pubkey filename matches Task 8's output**

Run: `ls /tmp/site/dist/apk/` and confirm the `*.rsa.pub` filename equals the one referenced in `dist/index.html` (`dwshell-dwshell-apk.rsa.pub`). Adjust the script's filename to match if needed, then re-run.
Expected: filenames match.

- [ ] **Step 4: Commit**

```bash
git add scripts/pages/render-index.sh
git commit -m "pages: install landing page generator"
```

---

### Task 10: Wire the Pages job into the release workflow

**Files:**
- Modify: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: the GitHub Release created by the `goreleaser` job (assets `*.deb`/`*.rpm`/`*.apk`); secrets `GPG_PRIVATE_KEY`, `APK_RSA_PRIVATE_KEY`; scripts from Tasks 6–9.
- Produces: a deployed Pages site.

- [ ] **Step 1: Add workflow-level Pages concurrency** below the existing `permissions:` block

```yaml
concurrency:
  group: pages-${{ github.workflow }}
  cancel-in-progress: false
```

- [ ] **Step 2: Add the `pages` job** after the `goreleaser` job

```yaml
  pages:
    needs: goreleaser
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pages: write
      id-token: write
    environment:
      name: github-pages
      url: ${{ steps.deploy.outputs.page_url }}
    steps:
      - uses: actions/checkout@v7

      - name: Install repo tooling
        run: sudo apt-get update -qq && sudo apt-get install -y -qq apt-utils dpkg-dev createrepo-c rpm gnupg

      - name: Download release packages
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          mkdir -p pkgs
          gh release download "${{ github.ref_name }}" -R "${{ github.repository }}" \
            --pattern '*.deb' --pattern '*.rpm' --pattern '*.apk' --dir pkgs
          ls -l pkgs

      - name: Import GPG signing key
        run: |
          echo "${{ secrets.GPG_PRIVATE_KEY }}" | gpg --batch --import
          echo "GPG_FPR=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')" >> "$GITHUB_ENV"

      - name: Write APK RSA key
        run: |
          install -m 700 -d "$HOME/keys"
          printf '%s' "${{ secrets.APK_RSA_PRIVATE_KEY }}" > "$HOME/keys/dwshell-apk.rsa"
          chmod 600 "$HOME/keys/dwshell-apk.rsa"
          openssl rsa -in "$HOME/keys/dwshell-apk.rsa" -pubout -out "$HOME/keys/dwshell-apk.rsa.pub"

      - name: Build APT repo
        run: scripts/pages/build-apt.sh pkgs site

      - name: Build YUM repo
        run: scripts/pages/build-yum.sh pkgs site

      - name: Build APK repo (Alpine container)
        run: |
          docker run --rm \
            -v "$PWD/pkgs:/pkgs" -v "$PWD/site:/site" \
            -v "$HOME/keys/dwshell-apk.rsa:/key.rsa" \
            -v "$HOME/keys/dwshell-apk.rsa.pub:/key.rsa.pub" \
            -v "$PWD/scripts/pages/build-apk.sh:/build-apk.sh" \
            alpine:latest sh -c "apk add --no-cache abuild >/dev/null && sh /build-apk.sh /pkgs /site /key.rsa"
          sudo chown -R "$USER" site

      - name: Render landing pages
        run: scripts/pages/render-index.sh site

      - name: Upload Pages artifact
        uses: actions/upload-pages-artifact@v3
        with:
          path: site

      - name: Deploy to Pages
        id: deploy
        uses: actions/deploy-pages@v4
```

- [ ] **Step 3: Validate the workflow YAML parses**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml')); print('YAML ok')"`
Expected: `YAML ok`.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "release: assemble and deploy signed package repos to Pages"
```

---

### Task 11: Document installation in the README

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: the public URLs/commands from Tasks 6–9.
- Produces: an Installation section users read first.

- [ ] **Step 1: Add an `## Installation` section** near the top of `README.md` (before Usage) with the four channels

```markdown
## Installation

**Homebrew (macOS & Linux)**
```sh
brew install porech/tap/dwshell
```

**Debian / Ubuntu (APT)**, **Fedora / RHEL (DNF)**, **Alpine (APK)**: signed
repositories are hosted at <https://porech.github.io/dwshell/dist/>. Copy-paste
setup for each is on that page.

**Direct download**: pre-built binaries for every platform are attached to each
[release](https://github.com/porech/dwshell/releases).

> The Homebrew tap is pushed by CI using a token seeded from a maintainer
> account; it is intended to be replaced with a dedicated fine-grained PAT
> (contents:write on `porech/homebrew-tap`).
```

- [ ] **Step 2: Verify it renders (no broken fences)**

Run: `grep -n "## Installation" README.md`
Expected: the heading is present; eyeball the fenced blocks are balanced.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: add installation instructions for all channels"
```

---

### Task 12: End-to-end release verification

**Files:** none (operational verification of the whole pipeline).

**Interfaces:**
- Consumes: everything above.
- Produces: a confirmed working release across all channels.

- [ ] **Step 1: Push the accumulated commits and confirm CI is green**

```bash
git push origin main
gh run watch "$(gh run list --workflow=ci.yml --limit 1 --json databaseId -q '.[0].databaseId')" --exit-status
```
Expected: CI passes.

- [ ] **Step 2: Tag a release and push it**

```bash
git tag -a v1.3.0 -m "v1.3.0 — package distribution (Homebrew + APT/YUM/APK)"
git push origin v1.3.0
```

- [ ] **Step 3: Watch the release workflow to completion**

```bash
gh run watch "$(gh run list --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')" --exit-status
```
Expected: `test`, `goreleaser`, and `pages` jobs all succeed.

- [ ] **Step 4: Confirm the formula landed and Pages is live**

```bash
gh api repos/porech/homebrew-tap/contents/Formula/dwshell.rb -q .name
curl -fsSL https://porech.github.io/dwshell/dist/apt/key.asc | head -1
curl -fsSL https://porech.github.io/dwshell/dist/yum/dwshell.repo | head -1
```
Expected: `dwshell.rb`; the APT key PEM header; the `.repo` header.

- [ ] **Step 5: Install end-to-end in throwaway containers**

```bash
# Debian
docker run --rm debian:stable bash -c 'apt-get update -qq && apt-get install -y -qq curl ca-certificates gnupg >/dev/null && install -m 0755 -d /etc/apt/keyrings && curl -fsSL https://porech.github.io/dwshell/dist/apt/key.asc -o /etc/apt/keyrings/dwshell.asc && echo "deb [signed-by=/etc/apt/keyrings/dwshell.asc] https://porech.github.io/dwshell/dist/apt ./" > /etc/apt/sources.list.d/dwshell.list && apt-get update -qq && apt-get install -y -qq dwshell && dwshell version'
# Fedora
docker run --rm fedora:latest bash -c 'curl -fsSL https://porech.github.io/dwshell/dist/yum/dwshell.repo -o /etc/yum.repos.d/dwshell.repo && dnf install -y -q dwshell && dwshell version'
# Alpine
docker run --rm alpine:latest sh -c 'curl -fsSL https://porech.github.io/dwshell/dist/apk/dwshell-dwshell-apk.rsa.pub -o /etc/apk/keys/dwshell.rsa.pub && echo https://porech.github.io/dwshell/dist/apk >> /etc/apk/repositories && apk update && apk add dwshell && dwshell version'
```
Expected: each prints `dwshell v1.3.0 ...`.

- [ ] **Step 6: (macOS, if available) verify Homebrew**

```bash
brew install porech/tap/dwshell && dwshell version
```
Expected: installs and prints the version.

---

## Self-Review

**Spec coverage:**
- Homebrew tap → Task 3 (config) + Task 5 (repo/secret) + Task 12 (verify). ✓
- APT/YUM/APK signed repos → Tasks 6/7/8 + Task 10 (CI wiring) + Task 12. ✓
- Pages under `/dist`, root placeholder → Task 9. ✓
- Dedicated GPG + RSA keys, local backups → Task 4. ✓
- Secrets + Pages enablement + autonomous bootstrap → Task 5. ✓
- Latest-only rebuild → Tasks 6–8 `rm -rf` the repo dir each run; Task 10 pulls only the current tag's assets. ✓
- `.gitignore` key guard → Task 1. ✓
- HOMEBREW token recommendation documented → Task 5 note + Task 11. ✓
- Validation plan (snapshot, lint, container installs) → Tasks 2–9 local validation + Task 12 E2E. ✓

**Placeholder scan:** No TBD/TODO; every script and config block is complete and literal.

**Type/name consistency:** Script contract `<in_dir> <out_dir>` consistent across Tasks 6–9 and Task 10 invocations. APK pubkey filename `dwshell-dwshell-apk.rsa.pub` is produced in Task 8 (`dwshell-$(basename key).pub` where key=`dwshell-apk.rsa`) and referenced identically in Task 9 and Task 12 (Step 3 of Task 9 explicitly reconciles it). Output tree `site/dist/{apt,yum,apk}` consistent between scripts and the upload step. `GPG_FPR` produced in Task 4, consumed in Tasks 6/7 and re-derived in the CI job (Task 10).

**Known fragility flagged:** RPM signing via `rpm --addsign` on Ubuntu depends on the `.rpmmacros` loopback config (Task 7) — verified by `rpm --checksig` in Task 7 Step 4 before it ever runs in CI.
