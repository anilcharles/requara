# Keycloak group /engineering/speklo-dev: read/write dev and staging secrets.
path "secret/data/speklo/dev/*"      { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/data/speklo/staging/*"  { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/metadata/speklo/*"      { capabilities = ["list", "read"] }
