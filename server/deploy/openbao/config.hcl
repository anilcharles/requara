storage "file" {
  path = "/openbao/data"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true # only reachable inside the compose network
}

api_addr      = "http://openbao:8200"
disable_mlock = true
ui            = false
