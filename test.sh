#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--local" ]]; then
  API_URL=http://127.0.0.1:8080
else
  API_URL=$(aws cloudformation describe-stacks --stack-name BinaryLambdaStack \
    --query "Stacks[0].Outputs[?OutputKey=='ApiUrl'].OutputValue | [0]" --output text)
fi
API_URL=${API_URL%/}

curl --fail -sS -X POST "$API_URL/api/json" \
--header 'user-agent: Dart/3.11 (dart:io)' \
--header 'content-type: application/json' \
--header 'accept-encoding: gzip' \
--header 'content-length: 505' \
--data-raw '{"type":"meetings","time":"2026-11-26T08:18:00-05:00","timeEnd":"2026-11-26T08:19:00-05:00","payload":"DRQbIikwNz5FTFNaYWhvdn2Ei5KZoKeutbzDytHY3+bt9PsCCRAXHiUsMzpBSE9WXWRrcnmAh46VnKOqsbi/xs3U2+Lp8Pf+BQwTGiEoLzY9REtSWWBnbnV8g4qRmJ+mrbS7wsnQ197l7PP6AQgPFh0kKzI5QEdOVVxjanF4f4aNlJuiqbC3vsXM09rh6O/2/QQLEhkgJy41PENKUVhfZm10e4KJkJeepayzusHIz9bd5Ovy+QAHDhUcIyoxOD9GTVRbYmlwd36FjJOaoaivtr3Ey9LZ4Ofu9fwDChEYHyYtNDtCSVBXXmVsc3qBiI+WnaSrsrnAx87V3OPq8fj/Bg0UGyIpMDc+RUxTWmFob3Z9hIuSmaCnrrW8w8rR2N/m7fT7AgkQFx4lLDM6"}' \
  | jq

echo '{"type":"meetings","time":"2026-11-26T08:18:00-05:00","timeEnd":"2026-11-26T08:19:00-05:00","payload":"DRQbIikwNz5FTFNaYWhvdn2Ei5KZoKeutbzDytHY3+bt9PsCCRAXHiUsMzpBSE9WXWRrcnmAh46VnKOqsbi/xs3U2+Lp8Pf+BQwTGiEoLzY9REtSWWBnbnV8g4qRmJ+mrbS7wsnQ197l7PP6AQgPFh0kKzI5QEdOVVxjanF4f4aNlJuiqbC3vsXM09rh6O/2/QQLEhkgJy41PENKUVhfZm10e4KJkJeepayzusHIz9bd5Ovy+QAHDhUcIyoxOD9GTVRbYmlwd36FjJOaoaivtr3Ey9LZ4Ofu9fwDChEYHyYtNDtCSVBXXmVsc3qBiI+WnaSrsrnAx87V3OPq8fj/Bg0UGyIpMDc+RUxTWmFob3Z9hIuSmaCnrrW8w8rR2N/m7fT7AgkQFx4lLDM6"}' | \
gzip | \
curl --fail -sS -X POST "$API_URL/api/jsonzip" \
--header 'user-agent: Dart/3.11 (dart:io)' \
--header 'content-type: application/json' \
--header 'content-encoding: gzip' \
--data-binary @- | jq

# Proto batch test
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

{
  for i in {1..3}; do
    printf 'entries {\n  type: "meetings"\n  time { seconds: 1764154680 }\n  time_end { seconds: 1764154740 }\n  payload: "DRQbIikwNz5FTFNaYWhvdn2Ei5KZoKeutbzDytHY3+bt9PsCCRAXHiUsMzpBSE9WXWRrcnmAh46VnKOqsbi/xs3U2+Lp8Pf+BQwTGiEoLzY9REtSWWBnbnV8g4qRmJ+mrbS7wsnQ197l7PP6AQgPFh0kKzI5QEdOVVxjanF4f4aNlJuiqbC3vsXM09rh6O/2/QQLEhkgJy41PENKUVhfZm10e4KJkJeepayzusHIz9bd5Ovy+QAHDhUcIyoxOD9GTVRbYmlwd36FjJOaoaivtr3Ey9LZ4Ofu9fwDChEYHyYtNDtCSVBXXmVsc3qBiI+WnaSrsrnAx87V3OPq8fj/Bg0UGyIpMDc+RUxTWmFob3Z9hIuSmaCnrrW8w8rR2N/m7fT7AgkQFx4lLDM6"\n}\n'
    done
} | protoc --proto_path=proto --encode=lambda.Batch proto/input.proto > "$workdir/batch.pb"

gzip -c "$workdir/batch.pb" > "$workdir/batch.pb.gz"

echo "=== Proto Batch (uncompressed) ==="
  curl --fail -sS -X POST "$API_URL/api/protobatch" \
  -H 'Content-Type: application/protobuf' \
  --data-binary "@$workdir/batch.pb" | jq

echo "=== Proto Batch (gzip) ==="
  curl --fail -sS -X POST "$API_URL/api/protobatchzip" \
  -H 'Content-Type: application/protobuf' -H 'Content-Encoding: gzip' \
  --data-binary "@$workdir/batch.pb.gz" | jq

# workdir=$(mktemp -d)
# trap 'rm -rf "$workdir"' EXIT

# # All requests carry the same 300 bytes and metadata.
# head -c 300 /dev/urandom > "$workdir/data.bin"
# data=$(od -An -v -t o1 "$workdir/data.bin" | awk '{for (i = 1; i <= NF; i++) printf "\\%s", $i}')
# encoded=$(base64 < "$workdir/data.bin" | tr -d '\r\n')
# time='2024-01-01T00:00:00Z'
# time_end='2024-01-01T00:01:00Z'
# type='example'

# jq -n --arg data "$encoded" --arg time "$time" --arg time_end "$time_end" --arg type "$type" \
#   '{data: $data, time: $time, time_end: $time_end, type: $type}' > "$workdir/input.json"
# gzip -c "$workdir/input.json" > "$workdir/input.json.gz"

# boundary='lambda-test-boundary'
# {
#   printf -- '--%s\r\nContent-Disposition: form-data; name="file"; filename="data.bin"\r\nContent-Type: application/octet-stream\r\n\r\n' "$boundary"
#   cat "$workdir/data.bin"
#   printf '\r\n'
#   for name in time timeEnd type; do
#     case "$name" in time) value=$time ;; timeEnd) value=$time_end ;; type) value=$type ;; esac
#     printf -- '--%s\r\nContent-Disposition: form-data; name="%s"\r\n\r\n%s\r\n' "$boundary" "$name" "$value"
#   done
#   printf -- '--%s--\r\n' "$boundary"
# } | gzip -c > "$workdir/multipart.gz"

# printf 'data: "%s"\ntime { seconds: 1704067200 }\ntime_end { seconds: 1704067260 }\ntype: "%s"\n' "$data" "$type" |
#   protoc --proto_path=proto --encode=lambda.Payload proto/input.proto > "$workdir/input.pb"
# gzip -c "$workdir/input.pb" > "$workdir/input.pb.gz"

# echo "API: $API_URL"
# curl --fail -sS -X POST "$API_URL/api/json" \
#   -H 'Content-Type: application/json' --data-binary "@$workdir/input.json" | jq
# curl --fail -sS -X POST "$API_URL/api/jsonzip" \
#   -H 'Content-Type: application/json' -H 'Content-Encoding: gzip' \
#   --data-binary "@$workdir/input.json.gz" | jq
# curl --fail -sS -X POST "$API_URL/api/bin" \
#   -F "file=@$workdir/data.bin" -F "time=$time" -F "timeEnd=$time_end" -F "type=$type" | jq
# curl --fail -sS -X POST "$API_URL/api/binzip" \
#   -H "Content-Type: multipart/form-data; boundary=$boundary" -H 'Content-Encoding: gzip' \
#   --data-binary "@$workdir/multipart.gz" | jq
# curl --fail -sS -X POST "$API_URL/api/proto" \
#   -H 'Content-Type: application/protobuf' --data-binary "@$workdir/input.pb" | jq
# curl --fail -sS -X POST "$API_URL/api/protozip" \
#   -H 'Content-Type: application/protobuf' -H 'Content-Encoding: gzip' \
#   --data-binary "@$workdir/input.pb.gz" | jq
