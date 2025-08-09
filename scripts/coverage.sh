#!/usr/bin/env bash
set -e

go test ./... -coverprofile=coverage.out
percent=$(go tool cover -func=coverage.out | grep total: | awk '{print $3}')
clean=${percent%\%}
url="https://img.shields.io/badge/coverage-${clean}%25-brightgreen"
curl -s "$url" -o coverage.svg
