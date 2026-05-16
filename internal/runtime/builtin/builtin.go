// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package builtin

import (
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/agentstep"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/archive"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/artifact"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/chat"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/command"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/dag"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/data"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/docker"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/file"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/harness"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/http"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/jq"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/kubernetes"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/log"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/mail"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/noop"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/redis"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/router"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/s3"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/sql"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/sql/drivers/duckdb"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/sql/drivers/postgres"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/sql/drivers/sqlite"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/ssh"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/template"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/wait"
)
