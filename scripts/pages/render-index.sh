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
<p><a href="dist/">Installation &amp; package repositories &rarr;</a> &middot;
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
<pre><code>sudo curl -fsSL https://porech.github.io/dwshell/dist/apk/dwshell.rsa.pub -o /etc/apk/keys/dwshell.rsa.pub
echo "https://porech.github.io/dwshell/dist/apk" | sudo tee -a /etc/apk/repositories
sudo apk update &amp;&amp; sudo apk add dwshell</code></pre>

<h2>Direct download</h2>
<p>Pre-built binaries for every platform are attached to each
<a href="https://github.com/porech/dwshell/releases">GitHub release</a>.</p>
</body></html>
EOF
echo "index pages rendered under $out"
