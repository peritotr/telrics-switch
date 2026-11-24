// SPDX-License-Identifier: AGPL-3.0-only

// Package db embeds the SQL migrations.
package db

import "embed"

//go:embed migrations/postgres/*.sql
var Postgres embed.FS

//go:embed migrations/clickhouse/*.sql
var ClickHouse embed.FS
