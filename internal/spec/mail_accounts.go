// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"net/mail"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/go-viper/mapstructure/v2"
)

// mailAccountConfig is one entry of the DAG-level mail_accounts map.
type mailAccountConfig struct {
	Provider string              `yaml:"provider"`
	IMAP     *mailServerConfig   `yaml:"imap"`
	SMTP     *mailServerConfig   `yaml:"smtp"`
	Username string              `yaml:"username"`
	Password string              `yaml:"password"`
	OAuth    *oauthconfig.Config `yaml:"oauth"`
}

type mailServerConfig struct {
	Host          string `yaml:"host"`
	Port          string `yaml:"port"`
	Security      string `yaml:"security"`
	SkipTLSVerify bool   `yaml:"skip_tls_verify"`
}

var (
	mailAccountKeys = []string{"provider", "imap", "smtp", "username", "password", "oauth"}
	mailServerKeys  = []string{"host", "port", "security", "skip_tls_verify"}
	mailOAuthKeys   = []string{"provider", "tenant_id", "client_id", "client_secret", "service_account_json", "refresh_token", "scopes"}
	// gmailUnusedKeys configure IMAP and SMTP, which a Gmail API account never uses.
	gmailUnusedKeys = []string{"imap", "smtp", "username"}
)

// mailProviderServers holds the servers each provider preset supplies.
var mailProviderServers = map[string]struct{ imap, smtp ir.MailServer }{
	ir.MailProviderGoogle: {
		imap: ir.MailServer{Host: "imap.gmail.com", Port: "993", Security: ir.MailSecurityTLS},
		smtp: ir.MailServer{Host: "smtp.gmail.com", Port: "465", Security: ir.MailSecurityTLS},
	},
	ir.MailProviderMicrosoft: {
		imap: ir.MailServer{Host: "outlook.office365.com", Port: "993", Security: ir.MailSecurityTLS},
		smtp: ir.MailServer{Host: "smtp.office365.com", Port: "587", Security: ir.MailSecurityStartTLS},
	},
}

// mailDefaultPorts maps a protocol and security mode to its standard port.
var mailDefaultPorts = map[string]map[string]string{
	"imap": {ir.MailSecurityTLS: "993", ir.MailSecurityStartTLS: "143"},
	"smtp": {ir.MailSecurityTLS: "465", ir.MailSecurityStartTLS: "587"},
}

func buildMailAccounts(_ buildContext, d *dag) (ir.MailAccounts, error) {
	if d.MailAccounts == nil {
		return nil, nil
	}

	accounts := make(ir.MailAccounts, len(d.MailAccounts))
	for key, value := range d.MailAccounts {
		address := strings.ToLower(strings.TrimSpace(key))
		if parsed, err := mail.ParseAddress(address); err != nil || parsed.Address != address {
			return nil, fmt.Errorf("mail account %q: not a valid email address", key)
		}
		if _, exists := accounts[address]; exists {
			return nil, fmt.Errorf("mail account %q is defined more than once", address)
		}
		account, err := parseMailAccount(value)
		if err != nil {
			return nil, fmt.Errorf("mail account %q: %w", address, err)
		}
		if account.Username == "" {
			account.Username = address
		}
		accounts[address] = account
	}
	return accounts, nil
}

func parseMailAccount(value any) (*ir.MailAccount, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be an object")
	}
	if err := checkMailKeys(raw, "", mailAccountKeys); err != nil {
		return nil, err
	}
	for _, block := range []string{"imap", "smtp"} {
		if nested, ok := raw[block].(map[string]any); ok {
			if err := checkMailKeys(nested, block+".", mailServerKeys); err != nil {
				return nil, err
			}
		}
	}
	if nested, ok := raw["oauth"].(map[string]any); ok {
		if err := checkMailKeys(nested, "oauth.", mailOAuthKeys); err != nil {
			return nil, err
		}
	}

	var cfg mailAccountConfig
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "yaml",
		WeaklyTypedInput: true,
		Result:           &cfg,
	})
	if err != nil {
		return nil, err
	}
	if err := decoder.Decode(raw); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	provider := strings.TrimSpace(cfg.Provider)
	if provider == "" {
		provider = ir.MailProviderIMAP
	}
	if provider != ir.MailProviderIMAP {
		if _, ok := mailProviderServers[provider]; !ok {
			return nil, fmt.Errorf("provider must be %s, %s, or %s",
				ir.MailProviderGoogle, ir.MailProviderMicrosoft, ir.MailProviderIMAP)
		}
	}

	hasPassword := strings.TrimSpace(cfg.Password) != ""
	if hasPassword == (cfg.OAuth != nil) {
		return nil, fmt.Errorf("set exactly one of password or oauth")
	}
	if err := oauthconfig.ValidateMailAccount(cfg.OAuth); err != nil {
		return nil, err
	}

	account := &ir.MailAccount{
		Provider: provider,
		Username: strings.TrimSpace(cfg.Username),
		Password: cfg.Password,
		OAuth:    cfg.OAuth,
	}
	if account.GmailAPI() {
		for _, key := range gmailUnusedKeys {
			if _, ok := raw[key]; ok {
				return nil, fmt.Errorf("%s is not used by a google account with oauth, which uses the Gmail API", key)
			}
		}
		return account, nil
	}

	preset := mailProviderServers[provider]
	imapServer, err := buildMailServer("imap", preset.imap, cfg.IMAP)
	if err != nil {
		return nil, err
	}
	if imapServer == nil || imapServer.Host == "" {
		return nil, fmt.Errorf("imap.host is required")
	}
	smtpServer, err := buildMailServer("smtp", preset.smtp, cfg.SMTP)
	if err != nil {
		return nil, err
	}
	if smtpServer != nil && smtpServer.Host == "" {
		smtpServer = nil
	}
	account.IMAP = imapServer
	account.SMTP = smtpServer
	return account, nil
}

// buildMailServer applies a server block over the provider preset. Fields the
// block leaves out keep the preset's values.
func buildMailServer(protocol string, preset ir.MailServer, block *mailServerConfig) (*ir.MailServer, error) {
	if block == nil {
		if preset.Host == "" {
			return nil, nil
		}
		return &preset, nil
	}

	server := preset
	if host := strings.TrimSpace(block.Host); host != "" {
		server.Host = host
	}
	security := strings.TrimSpace(block.Security)
	referenced := security != "" && isValueReference(security)
	switch {
	case security == "":
		if server.Security == "" {
			server.Security = ir.MailSecurityTLS
		}
	case security == ir.MailSecurityTLS, security == ir.MailSecurityStartTLS, referenced:
		server.Security = security
	default:
		return nil, fmt.Errorf("%s.security must be %s or %s", protocol, ir.MailSecurityTLS, ir.MailSecurityStartTLS)
	}

	switch port := strings.TrimSpace(block.Port); {
	case port != "":
		server.Port = port
	case referenced:
		// The standard port depends on a mode known only at run time.
		return nil, fmt.Errorf("%s.port is required when %s.security is a value reference", protocol, protocol)
	case security != "", server.Port == "":
		// A changed mode, or a server with no preset, takes the mode's standard port.
		server.Port = mailDefaultPorts[protocol][server.Security]
	}
	server.SkipTLSVerify = block.SkipTLSVerify
	return &server, nil
}

func checkMailKeys(raw map[string]any, prefix string, allowed []string) error {
	for key := range raw {
		if !slices.Contains(allowed, key) {
			return fmt.Errorf("unknown field %q", prefix+key)
		}
	}
	return nil
}
