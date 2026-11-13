#!/bin/sh
# Regenerates the key-mode fixtures of TestKeyRootRealCosign: a test cosign
# key pair (the private key encrypted with an empty password, then
# deleted), another public key, a marketplace "acme" whose index, manifest
# and binary are signed with the key and uploaded to Rekor, and an index
# bundle signed without the tlog upload. Needs cosign and network access to
# Rekor. Run it from this directory.
set -eu
cd "$(dirname "$0")"
rm -f cosign.key cosign.pub other.key other.pub index.json plugin.toml binary ./*.sigstore.json
COSIGN_PASSWORD= cosign generate-key-pair
COSIGN_PASSWORD= cosign generate-key-pair --output-key-prefix other
rm other.key
cat > plugin.toml <<'EOF'
name = "pdf.parses"
version = "1.0.0"
protocol = 1
question = "Does the file parse as a PDF without errors?"
one = "it parses"
zero = "it does not"
accepts = ["application/pdf"]
limits = { cpu_s = 5, memory = "64MiB", timeout_s = 5 }
EOF
printf 'synthetic plugin binary\n' > binary
u=https://plugins.acme.example/pdf.parses-1.0.0
man=$(sha256sum plugin.toml | cut -d' ' -f1)
bin=$(sha256sum binary | cut -d' ' -f1)
printf '{"schema":1,"marketplace":"acme","generated":"2026-11-13T00:00:00Z","plugins":[{"name":"pdf.parses","versions":[{"version":"1.0.0","manifest_url":"%s/plugin.toml","manifest_sha256":"%s","manifest_bundle_url":"%s/plugin.toml.sigstore.json","yanked":false,"artifacts":[{"os":"linux","arch":"amd64","url":"%s/binary","sha256":"%s","bundle_url":"%s/binary.sigstore.json"}]}]}]}\n' \
	"$u" "$man" "$u" "$u" "$bin" "$u" > index.json
for f in index.json plugin.toml binary; do
	COSIGN_PASSWORD= cosign sign-blob --yes --new-bundle-format --key cosign.key --bundle "$f.sigstore.json" "$f"
done
COSIGN_PASSWORD= cosign sign-blob --yes --new-bundle-format --key cosign.key \
	--tlog-upload=false --bundle index.notlog.sigstore.json index.json
rm cosign.key
