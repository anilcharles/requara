# Keycloak group /engineering/requara-dev: read/write dev and staging secrets.
path "secret/data/requara/dev/*"      { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/data/requara/staging/*"  { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/metadata/requara/*"      { capabilities = ["list", "read"] }
