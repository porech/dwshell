#!/usr/bin/env bash
# Build a signed YUM/DNF repository from a directory of .rpm files.
# Usage: build-yum.sh <rpm_dir> <out_dir>   (requires GPG_FPR, rpm, createrepo_c, gnupg)
set -euo pipefail
rpm_dir="${1:?rpm dir}"; out="${2:?out dir}"
yum="$out/dist/yum"
rm -rf "$yum"; mkdir -p "$yum"
cp "$rpm_dir"/*.rpm "$yum/"

# Sign the RPM packages so dnf can enforce gpgcheck=1.
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
