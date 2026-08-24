#!/usr/bin/env sh
# Build a signed APK repository. Run inside alpine with abuild installed.
# Usage: build-apk.sh <apk_dir> <out_dir> <rsa_key_path>
# Publishes the public key as dwshell.rsa.pub; users drop it in /etc/apk/keys/.
set -eu
apk_dir="$1"; out="$2"; key="$3"
base="$out/dist/apk"
rm -rf "$base"; mkdir -p "$base"

# Give the key a branded, stable name so the signature embeds dwshell.rsa.pub.
keydir="$(mktemp -d)"
cp "$key" "$keydir/dwshell.rsa"
if [ -f "$key.pub" ]; then
  cp "$key.pub" "$keydir/dwshell.rsa.pub"
else
  openssl rsa -in "$keydir/dwshell.rsa" -pubout -out "$keydir/dwshell.rsa.pub"
fi
cp "$keydir/dwshell.rsa.pub" "$base/dwshell.rsa.pub"

map_arch() { case "$1" in *amd64*) echo x86_64 ;; *arm64*) echo aarch64 ;; *) echo "" ;; esac; }
for f in "$apk_dir"/*.apk; do
  a="$(map_arch "$f")"; [ -n "$a" ] || continue
  mkdir -p "$base/$a"; cp "$f" "$base/$a/"
done
for d in "$base"/*/; do
  [ -d "$d" ] || continue
  ( cd "$d" && apk index --allow-untrusted -o APKINDEX.tar.gz ./*.apk && abuild-sign -k "$keydir/dwshell.rsa" APKINDEX.tar.gz )
done
echo "APK repo built at $base"
