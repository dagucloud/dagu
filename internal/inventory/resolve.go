// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	// MaxNameLength is the longest inventory name, in characters.
	MaxNameLength = 200

	// maxNodeNameLength keeps a node name short enough to name the file of
	// its trusted host key, with room for a temporary file's suffix.
	maxNodeNameLength = 200

	defaultPort    = 22
	defaultTimeout = 30 * time.Second
	maxHostLength  = 253
)

var (
	// ErrInvalid reports an inventory that breaks a rule. Every error from
	// Resolve wraps it.
	ErrInvalid = errors.New("invalid inventory")
	// ErrEmptySelector reports a selector with no terms.
	ErrEmptySelector = errors.New("node selector is empty")

	nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	userPattern     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// Error is a broken rule, located by inventory, node, and field.
type Error struct {
	Inventory string
	Node      string
	Field     string
	Err       error
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "inventory %q", e.Inventory)
	if e.Node != "" {
		fmt.Fprintf(&b, ": node %q", e.Node)
	}
	if e.Field != "" {
		fmt.Fprintf(&b, ": %s", e.Field)
	}
	fmt.Fprintf(&b, ": %v", e.Err)
	return b.String()
}

func (e *Error) Unwrap() []error { return []error{ErrInvalid, e.Err} }

// Resolve checks a project's inventories against every rule and returns their
// nodes with defaults applied, in name order. It reports every broken rule.
func Resolve(inventories []Inventory) ([]ResolvedNode, error) {
	r := resolver{nodeOwners: make(map[string]nodeOwner)}
	var nodes []ResolvedNode
	for _, inv := range inventories {
		nodes = append(nodes, r.inventory(inv)...)
	}
	if len(r.errs) > 0 {
		return nil, errors.Join(r.errs...)
	}
	slices.SortFunc(nodes, func(a, b ResolvedNode) int { return strings.Compare(a.Name, b.Name) })
	return nodes, nil
}

type resolver struct {
	// inventoryNames lists the names seen so far. Inventory names may hold
	// any text, so they are compared with strings.EqualFold.
	inventoryNames []string
	// nodeOwners maps a lowercased node name to the inventory that holds it,
	// so a clash can name the other inventory. Node names are ASCII.
	nodeOwners map[string]nodeOwner
	// position counts the inventories resolved so far.
	position int
	errs     []error
}

type nodeOwner struct {
	inventory string
	position  int
}

func (r *resolver) fail(inv, node, field string, err error) {
	r.errs = append(r.errs, &Error{Inventory: inv, Node: node, Field: field, Err: err})
}

func (r *resolver) inventory(inv Inventory) []ResolvedNode {
	r.position++
	name := inv.Name
	switch {
	case strings.TrimSpace(name) == "":
		r.fail(name, "", "name", errors.New("is required"))
	case utf8.RuneCountInString(name) > MaxNameLength:
		r.fail(name, "", "name", fmt.Errorf("is longer than %d characters", MaxNameLength))
	default:
		i := slices.IndexFunc(r.inventoryNames, func(other string) bool { return strings.EqualFold(other, name) })
		if i >= 0 {
			r.fail(name, "", "name", fmt.Errorf("is also the name of inventory %q", r.inventoryNames[i]))
		} else {
			r.inventoryNames = append(r.inventoryNames, name)
		}
	}

	defaults := r.connection(name, "", "defaults.", inv.Defaults.Connection)
	defaultLabels := r.labels(name, "", "defaults.labels", inv.Defaults.Labels)

	// A bastion names its node exactly as written. Only the uniqueness of
	// node names ignores case.
	local := make(map[string]Node, len(inv.Nodes))
	for _, n := range inv.Nodes {
		local[n.Name] = n
	}
	// The default bastion is one setting, so it is checked once rather than
	// for every node that uses it.
	if b := defaults.Bastion; b != "" {
		if err := bastionError(local, b, b); err != nil {
			r.fail(name, "", "defaults.bastion", err)
		}
	}

	nodes := make([]ResolvedNode, 0, len(inv.Nodes))
	for _, n := range inv.Nodes {
		nodes = append(nodes, r.node(inv, n, local, defaults, defaultLabels))
	}
	return nodes
}

// resolvedConnection is a connection whose credentials are parsed.
type resolvedConnection struct {
	Connection
	auth ir.SSHAuth
}

func (r *resolver) connection(inv, node, prefix string, c Connection) resolvedConnection {
	fail := func(field string, err error) { r.fail(inv, node, prefix+field, err) }
	// Port 0 leaves the port unset, so the default applies.
	if c.Port < 0 || c.Port > 65535 {
		fail("port", fmt.Errorf("%d is not between 1 and 65535", c.Port))
	}
	if c.User != "" && !userPattern.MatchString(c.User) {
		fail("user", fmt.Errorf("%q must match %s", c.User, userPattern))
	}
	if c.Timeout != "" {
		if d, err := time.ParseDuration(c.Timeout); err != nil || d <= 0 {
			fail("timeout", fmt.Errorf("%q is not a positive duration", c.Timeout))
		}
	}
	if c.HostKey != "" {
		if _, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(c.HostKey)); err != nil || len(options) > 0 || len(rest) > 0 {
			fail("host_key", errors.New("must be one public key in authorized_keys form, such as ssh-ed25519 AAAA… comment"))
		}
	}
	auth, err := ir.ParseSSHAuth("", c.Key, c.Password, c.Passphrase)
	for _, e := range credentialErrors(err) {
		fail(e.field, e.err)
	}
	// Literal passwords are rejected, so an exported inventory holds no
	// secret and is safe to share. The raw values are checked, so this is
	// reported even when another credential fails to parse.
	if s, isString := c.Password.(string); isString && s != "" {
		fail("password", errors.New(`takes only {"secret": <ref>}`))
	}
	if s, isString := c.Passphrase.(string); isString && s != "" {
		fail("passphrase", errors.New(`takes only {"secret": <ref>}`))
	}
	return resolvedConnection{Connection: c, auth: auth}
}

type fieldError struct {
	field string
	err   error
}

// credentialErrors splits ir.ParseSSHAuth's errors into fields relative to
// the connection, such as key.secret.
func credentialErrors(err error) []fieldError {
	if err == nil {
		return nil
	}
	var list ir.ErrorList
	if !errors.As(err, &list) {
		list = ir.ErrorList{err}
	}
	out := make([]fieldError, 0, len(list))
	for _, e := range list {
		var ve *ir.ValidationError
		if !errors.As(e, &ve) {
			out = append(out, fieldError{err: e})
			continue
		}
		cause := ve.Err
		if ve.Value != nil {
			cause = fmt.Errorf("%w (value: %v)", ve.Err, ve.Value)
		}
		out = append(out, fieldError{field: strings.TrimPrefix(ve.Field, "."), err: cause})
	}
	return out
}

func (r *resolver) labels(inv, node, field string, raw []string) ir.Labels {
	labels := make(ir.Labels, 0, len(raw))
	for _, s := range raw {
		label := ir.ParseLabel(s)
		if err := ir.ValidateLabel(label); err != nil {
			r.fail(inv, node, field, err)
			continue
		}
		labels = append(labels, label)
	}
	return labels
}

func (r *resolver) node(inv Inventory, n Node, local map[string]Node, defaults resolvedConnection, defaultLabels ir.Labels) ResolvedNode {
	fail := func(field string, err error) { r.fail(inv.Name, n.Name, field, err) }

	switch {
	case !nodeNamePattern.MatchString(n.Name):
		fail("name", fmt.Errorf("%q must match %s", n.Name, nodeNamePattern))
	case len(n.Name) > maxNodeNameLength:
		fail("name", fmt.Errorf("is longer than %d characters", maxNodeNameLength))
	default:
		// Names are compared without case, since trusted host keys are stored
		// by node name and some file systems ignore case.
		folded := strings.ToLower(n.Name)
		switch owner, dup := r.nodeOwners[folded]; {
		case !dup:
			r.nodeOwners[folded] = nodeOwner{inventory: inv.Name, position: r.position}
		case owner.position == r.position:
			fail("name", errors.New("is also the name of another node of this inventory"))
		default:
			fail("name", fmt.Errorf("is also a node of inventory %q", owner.inventory))
		}
	}
	switch {
	case n.Host == "":
		fail("host", errors.New("is required"))
	case !validHost(n.Host):
		fail("host", fmt.Errorf("%q is not a DNS name, an IPv4 address, or an IPv6 address", n.Host))
	}

	own := r.connection(inv.Name, n.Name, "", n.Connection)
	labels := r.labels(inv.Name, n.Name, "labels", n.Labels)

	c := merge(defaults, own)
	switch n.Bastion {
	case "":
		// The default bastion was checked with the defaults. A node is never
		// its own bastion.
		if c.Bastion == n.Name {
			c.Bastion = ""
		}
	case n.Name:
		fail("bastion", errors.New("names the node itself"))
	default:
		if err := bastionError(local, defaults.Bastion, n.Bastion); err != nil {
			fail("bastion", err)
		}
	}

	timeout := defaultTimeout
	if c.Timeout != "" {
		timeout, _ = time.ParseDuration(c.Timeout)
	}
	return ResolvedNode{
		Name:          n.Name,
		Host:          n.Host,
		InventoryID:   inv.ID,
		InventoryName: inv.Name,
		Port:          cmp.Or(c.Port, defaultPort),
		User:          c.User,
		Auth:          c.auth,
		Bastion:       c.Bastion,
		HostKey:       c.HostKey,
		StrictHostKey: c.StrictHostKey == nil || *c.StrictHostKey,
		Shell:         c.Shell,
		Timeout:       timeout,
		Labels:        mergeLabels(defaultLabels, labels),
		Description:   n.Description,
		Disabled:      n.Disabled,
	}
}

// bastionError reports why bastion cannot be a hop in its inventory, given
// the inventory's default bastion. A bastion must connect directly.
func bastionError(local map[string]Node, defaultBastion, bastion string) error {
	hop, found := local[bastion]
	if !found {
		return fmt.Errorf("%q is not a node of this inventory", bastion)
	}
	// The hop uses its own bastion, or the default one unless that is itself.
	if hop.Bastion != "" || (defaultBastion != "" && defaultBastion != hop.Name) {
		return fmt.Errorf("%q connects through a bastion itself; only one hop is allowed", bastion)
	}
	return nil
}

// merge applies a node's own connection fields over the defaults. Credentials
// follow the rule base config follows (#3769): a node that sets a key also
// replaces the passphrase, which belongs to the key it unlocks.
func merge(defaults, own resolvedConnection) resolvedConnection {
	c := defaults
	c.Port = cmp.Or(own.Port, c.Port)
	c.User = cmp.Or(own.User, c.User)
	c.Bastion = cmp.Or(own.Bastion, c.Bastion)
	c.HostKey = cmp.Or(own.HostKey, c.HostKey)
	c.Shell = cmp.Or(own.Shell, c.Shell)
	c.Timeout = cmp.Or(own.Timeout, c.Timeout)
	if own.StrictHostKey != nil {
		c.StrictHostKey = own.StrictHostKey
	}
	if own.auth.HasKey() || own.auth.HasPassphrase() {
		c.auth.Passphrase, c.auth.PassphraseSecret = own.auth.Passphrase, own.auth.PassphraseSecret
	}
	if own.auth.HasKey() {
		c.auth.Key, c.auth.KeySecret = own.auth.Key, own.auth.KeySecret
	}
	if own.auth.HasPassword() {
		c.auth.Password, c.auth.PasswordSecret = own.auth.Password, own.auth.PasswordSecret
	}
	return c
}

// mergeLabels adds the default labels to a node's labels. A node label
// replaces a default label with the same key.
func mergeLabels(defaults, own ir.Labels) ir.Labels {
	merged := slices.Clone(own)
	for _, d := range defaults {
		if !own.HasKey(d.Key) {
			merged = append(merged, d)
		}
	}
	return merged
}

func validHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == ""
	}
	return len(host) <= maxHostLength && hostnamePattern.MatchString(host)
}

// ParseSelector parses a node selector: label filter terms separated by
// commas (Spec 087). A selector with no terms is an error.
func ParseSelector(selector string) ([]ir.LabelFilter, error) {
	filters, err := ir.ParseLabelFilters(strings.Split(selector, ","))
	if err != nil {
		return nil, err
	}
	if len(filters) == 0 {
		return nil, ErrEmptySelector
	}
	return filters, nil
}

// Select returns the nodes that match every filter, in the order given.
func Select(nodes []ResolvedNode, filters []ir.LabelFilter) []ResolvedNode {
	var matched []ResolvedNode
	for _, n := range nodes {
		if n.Labels.MatchesFilters(filters) {
			matched = append(matched, n)
		}
	}
	return matched
}
