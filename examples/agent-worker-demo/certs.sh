#!/bin/sh
# Make the demo certificates with openssl.
#
# usage: certs.sh [output directory]   (default: run/certs, next to this script)
#
# The script makes two CAs and these folders. Each folder holds ca.crt, the
# certificate <name>.crt and the key <name>.key. A service mounts only its own folder.
#
#   coordinator/    dagu-coordinator  server and client certificate. The CA is the demo CA.
#   agent-claude/   agent-claude      client certificate of the agent worker.
#   general-worker/ general-worker    client certificate of the general worker.
#   rogue/          rogue             client certificate. The CN is in no worker pool.
#   other-ca-client/ agent-claude     client certificate from a second CA. The coordinator
#                                     does not trust that CA.
#
# The keys have the mode 0400. The CA keys stay in ca/ and other-ca/, and no service mounts them.
# The script keeps the certificates that are valid for one more day. Set CERTS_FORCE=1 to
# make them again.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
out=${1:-$here/run/certs}
days=30

if [ "$(id -u)" = 10001 ]; then
  echo "certs.sh: the host user ID is 10001, the user ID of the restricted user of the demo" >&2
  exit 1
fi

if [ -z "${CERTS_FORCE:-}" ] && [ -f "$out/ca/ca.crt" ] && openssl x509 -checkend 86400 -noout -in "$out/ca/ca.crt" >/dev/null 2>&1; then
  echo "certs.sh: the certificates in $out are valid, nothing to do"
  exit 0
fi

# A failure must not leave a folder that the check above takes for complete.
finish() {
  status=$?
  if [ "$status" -ne 0 ]; then rm -rf "$out"; fi
}
trap finish EXIT
rm -rf "$out"
mkdir -p "$out"
chmod 0755 "$out"
umask 077

# make_ca <dir name> <common name>
make_ca() {
  mkdir -p "$out/$1"
  openssl ecparam -genkey -name prime256v1 -noout -out "$out/$1/ca.key"
  openssl req -x509 -new -key "$out/$1/ca.key" -sha256 -days "$days" -subj "/CN=$2" \
    -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -out "$out/$1/ca.crt"
}

# issue <ca dir> <target dir> <common name> <extended key usage> <subject alternative names or empty>
issue() {
  ca=$out/$1
  dir=$out/$2
  mkdir -p "$dir"
  openssl ecparam -genkey -name prime256v1 -noout -out "$dir/$3.key"
  openssl req -new -key "$dir/$3.key" -subj "/CN=$3" -out "$dir/$3.csr"
  {
    echo "basicConstraints=CA:FALSE"
    echo "keyUsage=critical,digitalSignature"
    echo "extendedKeyUsage=$4"
    if [ -n "$5" ]; then echo "subjectAltName=$5"; fi
  } > "$dir/$3.ext"
  openssl x509 -req -in "$dir/$3.csr" -CA "$ca/ca.crt" -CAkey "$ca/ca.key" -CAcreateserial \
    -sha256 -days "$days" -extfile "$dir/$3.ext" -out "$dir/$3.crt" >/dev/null 2>&1
  rm -f "$dir/$3.csr" "$dir/$3.ext"
  # Each service needs the CA of the coordinator to check the coordinator certificate.
  cp "$out/ca/ca.crt" "$dir/ca.crt"
  chmod 0400 "$dir/$3.key"
  chmod 0444 "$dir/$3.crt" "$dir/ca.crt"
  chmod 0755 "$dir"
}

make_ca ca "agent-worker-demo CA"
make_ca other-ca "other CA (the coordinator does not trust it)"

issue ca coordinator dagu-coordinator "serverAuth,clientAuth" "DNS:dagu-coordinator,DNS:localhost"
issue ca agent-claude agent-claude "clientAuth" ""
issue ca general-worker general-worker "clientAuth" ""
issue ca rogue rogue "clientAuth" ""
issue other-ca other-ca-client agent-claude "clientAuth" ""

# The CA keys are not for a service.
chmod 0700 "$out/ca" "$out/other-ca"
rm -f "$out"/*/*.srl

echo "certs.sh: certificates written to $out"
