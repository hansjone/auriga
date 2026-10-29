#!/bin/sh
# Per-process TLS trust only: no browser key, no host/system trust mutation.
set -eu
umask 077
test -s /run/auriga-collab-token/collab/service.jwt
keytool -importcert -noprompt -alias deployment-browser \
  -file /etc/auriga/collab/browser.crt -keystore /tmp/jwks.p12 \
  -storetype PKCS12 -storepass local-tls-trust >/dev/null 2>&1
exec java -Djavax.net.ssl.trustStore=/tmp/jwks.p12 \
  -Djavax.net.ssl.trustStorePassword=local-tls-trust \
  -jar /app/platform.jar --spring.config.additional-location=file:/etc/auriga/collab/collab.properties
