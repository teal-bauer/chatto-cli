#!/usr/bin/env bash
# Regenerates the vendored Go protobuf/Connect bindings in internal/pb from
# the sibling chatto monorepo's proto module.
#
# Requires: buf (https://buf.build/docs/installation), protoc-gen-go and
# protoc-gen-connect-go on PATH (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
# and go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest put them in
# $(go env GOPATH)/bin), and network access to the Buf Schema Registry for the
# protovalidate dependency.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
proto_dir="${repo_root}/../chatto/proto"

if [[ ! -d "${proto_dir}" ]]; then
  echo "error: expected sibling proto module at ${proto_dir}" >&2
  echo "       (chatto-cli must be checked out next to chatto/chatto)" >&2
  exit 1
fi

export PATH="$(go env GOPATH)/bin:${PATH}"

out_dir="${repo_root}/internal/pb"
rm -rf "${out_dir}"
mkdir -p "${out_dir}"

cd "${proto_dir}"
buf generate --template "${script_dir}/buf.gen.yaml"

echo "Generated Go protobuf/Connect bindings in ${out_dir}"
