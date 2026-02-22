ui = true

api_addr = "http://vault:8200"
cluster_addr = "http://vault:8201"
disable_mlock = true
default_lease_ttl = "1h"
max_lease_ttl = "24h"
log_level = "info"

listener "tcp" {
  address = "0.0.0.0:8200"
  tls_disable = 1
}

storage "file" {
  path = "/vault/file"
}
