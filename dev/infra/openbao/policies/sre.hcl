# Keycloak group /sre: full access to Requara secrets.
path "secret/data/requara/*"      { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/metadata/requara/*"  { capabilities = ["create", "read", "update", "delete", "list"] }
