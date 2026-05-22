// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mail

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/mailer"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/go-viper/mapstructure/v2"
)

var _ executor.Executor = (*mail)(nil)

type mail struct {
	stdout io.Writer
	stderr io.Writer
	mailer *mailer.Client
	cfg    *mailConfig
}

type mailConfig struct {
	From        string   `mapstructure:"from"`
	To          any      `mapstructure:"to"`
	Subject     string   `mapstructure:"subject"`
	Message     string   `mapstructure:"message"`
	Attachments []string `mapstructure:"attachments"`
}

func newMail(ctx context.Context, step core.Step) (executor.Executor, error) {
	var cfg mailConfig
	if err := decodeMailConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode mail config: %w", err)
	}

	env := runtime.NewEnv(ctx, step)

	exec := &mail{cfg: &cfg}
	mailerConfig, err := env.MailerConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to substitute string fields: %w", err)
	}

	exec.mailer = mailer.New(mailerConfig)

	return exec, nil
}

func (e *mail) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *mail) SetStderr(out io.Writer) {
	e.stderr = out
}

func (*mail) Kill(_ os.Signal) error {
	return nil
}

const mailLogTemplate = `sending email
-----
from: %s
to: %s
subject: %s
message: %s
-----
`

func (e *mail) Run(ctx context.Context) error {
	// Convert To field to []string
	var toAddresses []string
	switch v := e.cfg.To.(type) {
	case string:
		if v != "" {
			toAddresses = []string{v}
		}
	case []string:
		toAddresses = v
	case []any:
		for _, addr := range v {
			if str, ok := addr.(string); ok && str != "" {
				toAddresses = append(toAddresses, str)
			}
		}
	default:
		return fmt.Errorf("invalid type for 'to' field: expected string or array, got %T", v)
	}

	if len(toAddresses) == 0 {
		return fmt.Errorf("no valid recipients specified")
	}

	_, _ = fmt.Fprintf(e.stdout, mailLogTemplate, e.cfg.From, strings.Join(toAddresses, ", "), e.cfg.Subject, e.cfg.Message)
	err := e.mailer.Send(
		ctx,
		e.cfg.From,
		toAddresses,
		e.cfg.Subject,
		e.cfg.Message,
		e.cfg.Attachments,
	)
	if err != nil {
		_, _ = e.stderr.Write([]byte("error occurred."))
	} else {
		_, _ = e.stdout.Write([]byte("sending email succeed."))
	}
	return err
}

func decodeMailConfig(dat map[string]any, cfg *mailConfig) error {
	md, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		WeaklyTypedInput: true,
		ErrorUnused:      false,
		Result:           cfg,
	})
	return md.Decode(dat)
}

func init() {
	executor.RegisterExecutor("mail", newMail, nil, core.ExecutorCapabilities{})
}
