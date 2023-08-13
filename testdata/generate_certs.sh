#!/usr/bin/env bash


mkdir -p certs && cd certs

OPENSSL_SAN=${OPENSSL_SAN:-"DNS:localhost,IP:127.1.1.100"}

# Server Key and Cert
openssl genpkey -algorithm Ed25519 -out server.key
openssl req -new -x509 -sha256 -key server.key -out server.crt -days 3650 -subj '/CN=localhost' -addext "subjectAltName = $OPENSSL_SAN"

echo "Server cert and key created"
echo "==========================="
openssl x509 -noout -text -in server.crt
echo "==========================="

# Client Key and Cert
openssl genpkey -algorithm Ed25519 -out client.key
openssl req -new -key client.key -out client.csr -subj '/CN=<some client UUID>' 

# Sign it with the server cert
# IRL you wouldn't do this, the leaf cert for a server would not have the same key as the CA authority
# See https://github.com/joekir/YUBIHSM_mTLS_PKI as an example of that done more thoroughly
openssl x509 -req -in client.csr -CA server.crt -CAkey server.key -set_serial 01 -out client.crt 

echo "Client cert and key created"
echo "==========================="
openssl x509 -noout -text -in client.crt
echo "==========================="
