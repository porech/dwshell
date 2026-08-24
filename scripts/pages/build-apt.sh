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
