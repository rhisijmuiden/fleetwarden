// Package sso implements optional single sign-on with Microsoft Entra ID
// through OpenID Connect (authorization code flow with PKCE).
package sso

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// Authority is the Entra login endpoint. Tests point it at a fake provider.
var Authority = "https://login.microsoftonline.com"

var guidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidGUID reports whether s looks like an Entra tenant or client ID.
func ValidGUID(s string) bool { return guidRE.MatchString(s) }

// Config is the Entra application registration used for sign-in.
type Config struct {
	TenantID     string
	ClientID     string
	ClientSecret string
}

func (c Config) issuer() string {
	return strings.TrimRight(Authority, "/") + "/" + c.TenantID + "/v2.0"
}

// Identity is what FleetWarden learns about a user from the ID token.
type Identity struct {
	TenantID      string
	ObjectID      string
	Username      string
	Name          string
	Email         string
	Groups        []string
	Roles         []string
	GroupsOverage bool // the user is in too many groups for the token; use app roles instead
}

// ExternalID is the stable key of an Entra account in FleetWarden.
func (i Identity) ExternalID() string { return "entra:" + i.TenantID + ":" + i.ObjectID }

// Client performs the OIDC flow. Providers (discovery documents and signing
// keys) are cached per tenant.
type Client struct {
	mu        sync.Mutex
	providers map[string]cachedProvider
}

type cachedProvider struct {
	p   *oidc.Provider
	exp time.Time
}

// NewClient creates an SSO client.
func NewClient() *Client { return &Client{providers: map[string]cachedProvider{}} }

func (c *Client) provider(ctx context.Context, cfg Config) (*oidc.Provider, error) {
	if !ValidGUID(cfg.TenantID) || !ValidGUID(cfg.ClientID) {
		return nil, errors.New("tenant ID and client ID must be GUIDs")
	}
	iss := cfg.issuer()
	c.mu.Lock()
	cp, ok := c.providers[iss]
	c.mu.Unlock()
	if ok && time.Now().Before(cp.exp) {
		return cp.p, nil
	}
	// The provider keeps using ctx for key refreshes, so do not tie it to a request.
	p, err := oidc.NewProvider(oidc.ClientContext(context.WithoutCancel(ctx), nil), iss)
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	c.mu.Lock()
	c.providers[iss] = cachedProvider{p, time.Now().Add(time.Hour)}
	c.mu.Unlock()
	return p, nil
}

func oauthConfig(p *oidc.Provider, cfg Config, redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  redirect,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
}

// AuthURL returns the Entra sign-in URL.
func (c *Client) AuthURL(ctx context.Context, cfg Config, redirect, state, nonce, verifier string) (string, error) {
	p, err := c.provider(ctx, cfg)
	if err != nil {
		return "", err
	}
	return oauthConfig(p, cfg, redirect).AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "select_account")), nil
}

type claims struct {
	TenantID          string         `json:"tid"`
	ObjectID          string         `json:"oid"`
	PreferredUsername string         `json:"preferred_username"`
	Name              string         `json:"name"`
	Email             string         `json:"email"`
	Groups            []string       `json:"groups"`
	Roles             []string       `json:"roles"`
	ClaimNames        map[string]any `json:"_claim_names"`
}

// Exchange redeems the authorization code and verifies the ID token.
func (c *Client) Exchange(ctx context.Context, cfg Config, redirect, code, verifier, nonce string) (Identity, error) {
	p, err := c.provider(ctx, cfg)
	if err != nil {
		return Identity{}, err
	}
	tok, err := oauthConfig(p, cfg, redirect).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("token exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return Identity{}, errors.New("no id_token in token response")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("id token: %w", err)
	}
	if idt.Nonce != nonce {
		return Identity{}, errors.New("id token: nonce mismatch")
	}
	var cl claims
	if err := idt.Claims(&cl); err != nil {
		return Identity{}, err
	}
	if !strings.EqualFold(cl.TenantID, cfg.TenantID) || cl.ObjectID == "" {
		return Identity{}, errors.New("id token: wrong tenant or missing object ID")
	}
	id := Identity{
		TenantID: strings.ToLower(cl.TenantID), ObjectID: strings.ToLower(cl.ObjectID),
		Username: strings.ToLower(cl.PreferredUsername), Name: cl.Name, Email: cl.Email,
		Groups: cl.Groups, Roles: cl.Roles,
	}
	if _, over := cl.ClaimNames["groups"]; over {
		id.GroupsOverage = true
	}
	if id.Username == "" {
		id.Username = id.ObjectID
	}
	if id.Email == "" && strings.Contains(id.Username, "@") {
		id.Email = id.Username
	}
	return id, nil
}

// Test checks the configuration: tenant discovery and the client secret
// (through a client-credentials token request, which needs no API permissions).
func (c *Client) Test(ctx context.Context, cfg Config) error {
	p, err := c.provider(ctx, cfg)
	if err != nil {
		return err
	}
	cc := clientcredentials.Config{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, TokenURL: p.Endpoint().TokenURL,
		Scopes: []string{"https://graph.microsoft.com/.default"},
	}
	if _, err := cc.Token(ctx); err != nil {
		return fmt.Errorf("client secret: %w", err)
	}
	return nil
}

// ResolveRole picks the most privileged role among the mappings that match
// the identity, and the device-group scope of that role: all groups when any
// matching mapping with that role is unscoped, otherwise the union of their
// groups. It returns false when nothing matches.
func ResolveRole(id Identity, mappings []store.RoleMapping) (auth.Role, store.Scope, bool) {
	best, found := auth.Role(""), false
	has := func(list []string, v string) bool {
		for _, x := range list {
			if strings.EqualFold(x, v) {
				return true
			}
		}
		return false
	}
	var matched []store.RoleMapping
	for _, m := range mappings {
		r := auth.Role(m.Role)
		if !auth.ValidRole(r) {
			continue
		}
		if (m.Claim == "group" && has(id.Groups, m.Value)) || (m.Claim == "role" && has(id.Roles, m.Value)) {
			matched = append(matched, m)
			if !found || r.Rank() < best.Rank() {
				best, found = r, true
			}
		}
	}
	if !found {
		return best, store.Scope{}, false
	}
	scope := store.Scope{Groups: []int64{}}
	seen := map[int64]bool{}
	for _, m := range matched {
		if auth.Role(m.Role) != best {
			continue
		}
		if !m.Scoped || best == auth.RoleAdministrator {
			return best, store.Scope{All: true}, true
		}
		for _, g := range m.Groups {
			if !seen[g] {
				seen[g] = true
				scope.Groups = append(scope.Groups, g)
			}
		}
	}
	return best, scope, true
}
