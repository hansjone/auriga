#!/bin/sh
# Internal JWKS over mTLS; public issuer/origin stays installation-owned.
set -eu
umask 077
test -s /run/auriga-collab-token/collab/service.jwt
keytool -importcert -noprompt -alias nas-internal \
  -file /etc/auriga/collab/ca.crt -keystore /tmp/jwks.p12 \
  -storetype PKCS12 -storepass local-tls-trust >/dev/null 2>&1
openssl pkcs12 -export -name collab-iam \
  -in /etc/auriga/collab/client.crt -inkey /etc/auriga/collab/client.key \
  -certfile /etc/auriga/collab/ca.crt -out /tmp/jwks-client.p12 \
  -passout pass:local-tls-client
exec java -Djavax.net.ssl.trustStore=/tmp/jwks.p12 \
  -Djavax.net.ssl.trustStorePassword=local-tls-trust \
  -Djavax.net.ssl.keyStore=/tmp/jwks-client.p12 \
  -Djavax.net.ssl.keyStorePassword=local-tls-client \
  -jar /app/platform.jar --spring.config.additional-location=file:/etc/auriga/collab/collab.properties
