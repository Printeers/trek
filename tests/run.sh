#!/bin/bash
set -euxo pipefail
cd "$(dirname "$0")"

rm -rf output
mkdir -p output

(
  cd output

  function trek {
    go run ../.. "${@}"
  }

  TREK_VERSION=latest \
  TREK_MODEL_NAME=santas_warehouse \
  TREK_DATABASE_NAME=north_pole \
  TREK_DATABASE_USERS=santa,worker \
  trek init

  for file in ../stages/*; do
    cp "$file" santas_warehouse.dbm

    trek generate "$(basename "$file" | cut -d "-" -f 2 | cut -d "." -f 1)"
  done
)
