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

jq -n '{entries: [
  {type: "meetings", time: "2026-11-26T08:18:00-05:00", time_end: "2026-11-26T08:19:00-05:00", payload: "aGVsbG8="},
  {type: "meetings", time: "2026-11-26T08:19:00-05:00", time_end: "2026-11-26T08:20:00-05:00", payload: "d29ybGQ="}
]}' > "$workdir/batch.json"
gzip -c "$workdir/batch.json" > "$workdir/batch.json.gz"

echo "=== JSON Batch (uncompressed) ==="
curl --fail -sS -X POST "$API_URL/api/jsonbatch" \
  -H 'Content-Type: application/json' \
  --data-binary "@$workdir/batch.json" | jq

echo "=== JSON Batch (gzip) ==="
curl --fail -sS -X POST "$API_URL/api/jsonbatchzip" \
  -H 'Content-Type: application/json' -H 'Content-Encoding: gzip' \
  --data-binary "@$workdir/batch.json.gz" | jq

boundary='lambda-batch-boundary'
{
  for i in 0 1 2; do
    for field in type time timeEnd payload; do
      case "$field" in
        type) value=meetings ;;
        time) value=2026-11-26T08:18:00-05:00 ;;
        timeEnd) value=2026-11-26T08:19:00-05:00 ;;
        payload) value=aGVsbG8= ;;
      esac
      printf -- '--%s\r\nContent-Disposition: form-data; name="entries[%s][%s]"\r\n\r\n%s\r\n' "$boundary" "$i" "$field" "$value"
    done
  done
  printf -- '--%s--\r\n' "$boundary"
} > "$workdir/batch.multipart"
gzip -c "$workdir/batch.multipart" > "$workdir/batch.multipart.gz"

echo "=== Multipart Batch (uncompressed) ==="
curl --fail -sS -X POST "$API_URL/api/binbatch" \
  -H "Content-Type: multipart/form-data; boundary=$boundary" \
  --data-binary "@$workdir/batch.multipart" | jq

echo "=== Multipart Batch (gzip) ==="
curl --fail -sS -X POST "$API_URL/api/binbatchzip" \
  -H "Content-Type: multipart/form-data; boundary=$boundary" -H 'Content-Encoding: gzip' \
  --data-binary "@$workdir/batch.multipart.gz" | jq

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
