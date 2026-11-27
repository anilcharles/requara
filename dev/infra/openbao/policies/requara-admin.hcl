# Keycloak group /engineering/requara-admin: everything requara-dev can do, plus
# write (but not read) production secrets.
path "secret/data/requara/dev/*"      { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/data/requara/staging/*"  { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/data/requara/prod/*"     { capabilities = ["create", "update"] }
path "secret/metadata/requara/*"      { capabilities = ["list", "read"] }
