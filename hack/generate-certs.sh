#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERT_DIR="$ROOT_DIR/certs"

SERVICES=(
  "persys-scheduler"
  "compute-agent"
  "persysctl"
  "persys-gateway"
  "persys-federation"
  "persys-forgery"
  "persys-operator"
)
EXTRA_HOSTS=()
CA_CN="Persys Cloud CA"
ORG_NAME="Persys Cloud"
COUNTRY_CODE="US"
DAYS=3650
FORCE=0
MODE="auto"
CFSSL_EXPIRY_HOURS=0

usage() {
  cat <<'EOF'
Usage: ./generate-certs.sh [options]

Options:
  --out-dir DIR               Output directory (default: ./certs)
  --services a,b,c            Generate only selected services
  --hosts h1,h2               Extra SAN hosts/IPs for all service certs
  --days N                    Certificate validity in days (default: 3650)
  --force                     Regenerate CA and service certs from scratch
  --mode auto|cfssl|openssl   Preferred generator mode (default: auto)
  --ca-cn VALUE               Certificate authority CN
  --org VALUE                 Certificate organization
  --country VALUE             Certificate country code (2 chars)
  -h, --help                  Show this help

Examples:
  ./generate-certs.sh
  ./generate-certs.sh --hosts 10.0.0.15,dev.local --force
  ./generate-certs.sh --services persys-scheduler,persys-gateway --mode openssl
EOF
}

contains_service() {
  local wanted="$1"
  local item
  for item in "${SERVICES[@]}"; do
    if [[ "$item" == "$wanted" ]]; then
      return 0
    fi
  done
  return 1
}

dedupe_csv() {
  local csv="$1"
  local unique=()
  local seen=""
  local item
  IFS=',' read -r -a arr <<<"$csv"
  for item in "${arr[@]}"; do
    [[ -z "$item" ]] && continue
    if [[ ",$seen," != *",$item,"* ]]; then
      unique+=("$item")
      seen="${seen},${item}"
    fi
  done
  local IFS=","
  echo "${unique[*]}"
}

parse_services_arg() {
  local raw="$1"
  local parsed=()
  IFS=',' read -r -a parsed <<<"$raw"
  if [[ "${#parsed[@]}" -eq 0 ]]; then
    echo "No services provided to --services" >&2
    exit 1
  fi
  local filtered=()
  local svc
  for svc in "${parsed[@]}"; do
    if ! contains_service "$svc"; then
      echo "Unknown service: $svc" >&2
      echo "Allowed services: ${SERVICES[*]}" >&2
      exit 1
    fi
    filtered+=("$svc")
  done
  SERVICES=("${filtered[@]}")
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      [[ $# -lt 2 ]] && { echo "--out-dir requires a value" >&2; exit 1; }
      CERT_DIR="$2"
      shift 2
      ;;
    --services)
      [[ $# -lt 2 ]] && { echo "--services requires a value" >&2; exit 1; }
      parse_services_arg "$2"
      shift 2
      ;;
    --hosts)
      [[ $# -lt 2 ]] && { echo "--hosts requires a value" >&2; exit 1; }
      IFS=',' read -r -a EXTRA_HOSTS <<<"$(dedupe_csv "$2")"
      shift 2
      ;;
    --days)
      [[ $# -lt 2 ]] && { echo "--days requires a value" >&2; exit 1; }
      DAYS="$2"
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    --mode)
      [[ $# -lt 2 ]] && { echo "--mode requires a value" >&2; exit 1; }
      MODE="$2"
      shift 2
      ;;
    --ca-cn)
      [[ $# -lt 2 ]] && { echo "--ca-cn requires a value" >&2; exit 1; }
      CA_CN="$2"
      shift 2
      ;;
    --org)
      [[ $# -lt 2 ]] && { echo "--org requires a value" >&2; exit 1; }
      ORG_NAME="$2"
      shift 2
      ;;
    --country)
      [[ $# -lt 2 ]] && { echo "--country requires a value" >&2; exit 1; }
      COUNTRY_CODE="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if ! [[ "$DAYS" =~ ^[0-9]+$ ]]; then
  echo "--days must be an integer" >&2
  exit 1
fi
CFSSL_EXPIRY_HOURS=$((DAYS * 24))

if ! [[ "$COUNTRY_CODE" =~ ^[A-Za-z]{2}$ ]]; then
  echo "--country must be a 2-letter country code" >&2
  exit 1
fi

mkdir -p "$CERT_DIR"

if [[ "$FORCE" -eq 1 ]]; then
  echo "==> Force mode: clearing existing cert artifacts in $CERT_DIR"
  rm -f "$CERT_DIR"/*.pem "$CERT_DIR"/*.csr "$CERT_DIR"/*-key.pem "$CERT_DIR"/*-csr.json "$CERT_DIR"/openssl-*.cnf "$CERT_DIR"/ca.srl
fi

has_cfssl() {
  command -v cfssl >/dev/null 2>&1 && command -v cfssljson >/dev/null 2>&1
}

has_openssl() {
  command -v openssl >/dev/null 2>&1
}

try_install_cfssl() {
  if has_cfssl; then
    return 0
  fi

  if ! command -v go >/dev/null 2>&1; then
    return 1
  fi

  echo "==> CFSSL not found, attempting install via go install"
  if go install github.com/cloudflare/cfssl/cmd/cfssl@latest && go install github.com/cloudflare/cfssl/cmd/cfssljson@latest; then
    export PATH="$PATH:$(go env GOPATH)/bin"
    has_cfssl
    return $?
  fi

  return 1
}

pick_mode() {
  case "$MODE" in
    cfssl)
      if has_cfssl || try_install_cfssl; then
        echo "cfssl"
      else
        echo "Requested --mode cfssl, but cfssl/cfssljson are unavailable." >&2
        exit 1
      fi
      ;;
    openssl)
      if has_openssl; then
        echo "openssl"
      else
        echo "Requested --mode openssl, but openssl is unavailable." >&2
        exit 1
      fi
      ;;
    auto)
      if has_cfssl || try_install_cfssl; then
        echo "cfssl"
      elif has_openssl; then
        echo "openssl"
      else
        echo "No supported certificate tool found. Install one of: cfssl+cfssljson, openssl." >&2
        exit 1
      fi
      ;;
    *)
      echo "Invalid --mode: $MODE (expected auto|cfssl|openssl)" >&2
      exit 1
      ;;
  esac
}

build_cfssl_hosts_json() {
  local service="$1"
  local hosts=("localhost" "127.0.0.1" "::1" "$service")
  local host
  for host in "${EXTRA_HOSTS[@]}"; do
    [[ -z "$host" ]] && continue
    hosts+=("$host")
  done

  local output=""
  local first=1
  for host in "${hosts[@]}"; do
    if [[ "$first" -eq 0 ]]; then
      output+=", "
    fi
    output+="\"$host\""
    first=0
  done
  echo "$output"
}

build_openssl_alt_names() {
  local service="$1"
  local hosts=("localhost" "127.0.0.1" "::1" "$service")
  local host
  for host in "${EXTRA_HOSTS[@]}"; do
    [[ -z "$host" ]] && continue
    hosts+=("$host")
  done

  local dns_index=1
  local ip_index=1
  local out=""
  for host in "${hosts[@]}"; do
    if [[ "$host" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || [[ "$host" == *:* ]]; then
      out+="IP.${ip_index} = ${host}"$'\n'
      ip_index=$((ip_index + 1))
    else
      out+="DNS.${dns_index} = ${host}"$'\n'
      dns_index=$((dns_index + 1))
    fi
  done
  printf "%s" "$out"
}

generate_with_cfssl() {
  local config_file="$CERT_DIR/ca-config.json"
  local csr_file="$CERT_DIR/ca-csr.json"

  cat >"$config_file" <<EOF
{
  "signing": {
    "default": {
      "expiry": "${CFSSL_EXPIRY_HOURS}h",
      "usages": ["signing", "key encipherment", "server auth", "client auth"],
      "allow_subject_alt_names": true
    }
  }
}
EOF

  cat >"$csr_file" <<EOF
{
  "CN": "${CA_CN}",
  "key": {
    "algo": "rsa",
    "size": 2048
  },
  "names": [
    {
      "O": "${ORG_NAME}",
      "C": "${COUNTRY_CODE}"
    }
  ]
}
EOF

  if [[ ! -f "$CERT_DIR/ca.pem" || ! -f "$CERT_DIR/ca-key.pem" ]]; then
    echo "==> Generating CA with CFSSL"
    (cd "$CERT_DIR" && cfssl gencert -initca ca-csr.json | cfssljson -bare ca)
  else
    echo "==> Reusing existing CA in $CERT_DIR"
  fi

  local service
  for service in "${SERVICES[@]}"; do
    local service_csr="$CERT_DIR/${service}-csr.json"
    cat >"$service_csr" <<EOF
{
  "CN": "${service}",
  "key": {
    "algo": "rsa",
    "size": 2048
  },
  "names": [
    {
      "O": "${ORG_NAME}",
      "C": "${COUNTRY_CODE}"
    }
  ],
  "hosts": [$(build_cfssl_hosts_json "$service")]
}
EOF
    echo "==> Generating cert for ${service} with CFSSL"
    (cd "$CERT_DIR" && cfssl gencert -ca=ca.pem -ca-key=ca-key.pem -config=ca-config.json -profile=default "${service}-csr.json" | cfssljson -bare "${service}")
  done
}

generate_with_openssl() {
  local ca_key="$CERT_DIR/ca-key.pem"
  local ca_cert="$CERT_DIR/ca.pem"

  if [[ ! -f "$ca_key" || ! -f "$ca_cert" ]]; then
    echo "==> Generating CA with OpenSSL"
    openssl req -x509 -nodes -newkey rsa:2048 -sha256 \
      -days "$DAYS" \
      -keyout "$ca_key" \
      -out "$ca_cert" \
      -subj "/C=${COUNTRY_CODE}/O=${ORG_NAME}/CN=${CA_CN}" >/dev/null 2>&1
  else
    echo "==> Reusing existing CA in $CERT_DIR"
  fi

  local service
  for service in "${SERVICES[@]}"; do
    local service_key="$CERT_DIR/${service}-key.pem"
    local service_csr="$CERT_DIR/${service}.csr"
    local service_cert="$CERT_DIR/${service}.pem"
    local ext_file="$CERT_DIR/openssl-${service}.cnf"

    cat >"$ext_file" <<EOF
[ req ]
distinguished_name = req_distinguished_name
prompt = no
req_extensions = req_ext

[ req_distinguished_name ]
C = ${COUNTRY_CODE}
O = ${ORG_NAME}
CN = ${service}

[ req_ext ]
subjectAltName = @alt_names

[ alt_names ]
$(build_openssl_alt_names "$service")
EOF

    echo "==> Generating cert for ${service} with OpenSSL"
    openssl req -new -newkey rsa:2048 -nodes \
      -keyout "$service_key" \
      -out "$service_csr" \
      -config "$ext_file" >/dev/null 2>&1

    openssl x509 -req -sha256 \
      -in "$service_csr" \
      -CA "$ca_cert" \
      -CAkey "$ca_key" \
      -CAcreateserial \
      -out "$service_cert" \
      -days "$DAYS" \
      -extfile "$ext_file" \
      -extensions req_ext >/dev/null 2>&1
  done
}

selected_mode="$(pick_mode)"
echo "==> Certificate mode: ${selected_mode}"
echo "==> Output directory: ${CERT_DIR}"
echo "==> Services: ${SERVICES[*]}"

case "$selected_mode" in
  cfssl)
    if ! generate_with_cfssl; then
      if has_openssl; then
        echo "CFSSL generation failed, retrying with OpenSSL fallback..."
        generate_with_openssl
      else
        echo "CFSSL generation failed and OpenSSL is unavailable." >&2
        exit 1
      fi
    fi
    ;;
  openssl)
    generate_with_openssl
    ;;
esac

echo "==> Generated certificate files:"
find "$CERT_DIR" -maxdepth 1 -type f \( -name "*.pem" -o -name "*.csr" -o -name "*-csr.json" \) | sort
echo "Use ca.pem plus each <service>.pem and <service>-key.pem for mTLS."
