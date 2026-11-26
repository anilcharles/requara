# Keycloak group /engineering/speklo-admin: everything speklo-dev can do, plus
# write (but not read) production secrets.
path "secret/data/speklo/dev/*"      { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/data/speklo/staging/*"  { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/data/speklo/prod/*"     { capabilities = ["create", "update"] }
path "secret/metadata/speklo/*"      { capabilities = ["list", "read"] }
