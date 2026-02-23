module github.com/persys-dev/persys-cloud/persys-automation

go 1.24.13

require (
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.10.9
	github.com/persys-dev/persys-cloud/pkg v0.0.0-00010101000000-000000000000
	github.com/redis/go-redis/v9 v9.18.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/sirupsen/logrus v1.9.4
	google.golang.org/grpc v1.79.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-jose/go-jose/v4 v4.1.3 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.2.0 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.7 // indirect
	github.com/hashicorp/hcl v1.0.1-vault-7 // indirect
	github.com/hashicorp/vault/api v1.22.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.32.0 // indirect
	golang.org/x/time v0.12.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
)

replace github.com/persys-dev/persys-cloud/pkg => ../pkg

replace github.com/lib/pq => ./third_party/libpq

replace github.com/redis/go-redis/v9 => ./third_party/go-redis-v9
