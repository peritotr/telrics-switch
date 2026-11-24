#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# render-config <template> <output>: replaces every __NAME__ in the template
# with the value of the environment variable NAME. A placeholder whose
# variable is not set stops the start, so a half-configured service never runs.
set -eu
src=$1
dst=$2
cp "$src" "$dst.tmp"
for name in $(grep -o '__[A-Z][A-Z0-9_]*__' "$src" | sort -u); do
  var=${name#__}
  var=${var%__}
  eval "known=\${$var+yes}"
  if [ -z "$known" ]; then
    echo "render-config: $src needs $var, which is not set" >&2
    exit 1
  fi
  eval "value=\$$var"
  escaped=$(printf '%s' "$value" | sed 's/[&|\\]/\\&/g')
  sed -i "s|$name|$escaped|g" "$dst.tmp"
done
mv "$dst.tmp" "$dst"
