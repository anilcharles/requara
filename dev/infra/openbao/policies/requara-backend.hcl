# The Requara backend (AppRole). Reads its own platform secrets and uses
# Transit to encrypt/decrypt customer environment secrets.
path "secret/data/requara/dev/*" {
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
