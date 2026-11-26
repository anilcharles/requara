# Keycloak group /sre: full access to Speklo secrets.
path "secret/data/speklo/*"      { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/metadata/speklo/*"  { capabilities = ["create", "read", "update", "delete", "list"] }
