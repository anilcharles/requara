# The Requara API (AppRole). Reads its own settings and uses Transit to
# encrypt/decrypt the secrets people keep in environments.
path "secret/data/requara/prod/*" {
  capabilities = ["read"]
}
path "transit/keys/ws-*" {
  capabilities = ["create", "read", "update"]
}
path "transit/keys/sessions" {
  capabilities = ["create", "read", "update"]
}
path "transit/encrypt/*" {
  capabilities = ["update"]
}
path "transit/decrypt/*" {
  capabilities = ["update"]
}
