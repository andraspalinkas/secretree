#!/usr/bin/env bash
# The walkthrough lives in internal/demo/tour.sh (embedded as `secretree demo`).
exec "$(dirname "$0")/../internal/demo/tour.sh" "$@"
