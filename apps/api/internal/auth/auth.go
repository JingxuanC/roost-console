package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type User struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
}

func (u *User) EmailOrID() string {
	if u.Email != "" {
		return u.Email
	}
	return u.ID + "@roost.local"
}

type Verifier struct {
	baseURL string
	apiKey  string
	client  *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	user      *User
	expiresAt time.Time
}

func NewVerifier(baseURL, apiKey string, client *http.Client) *Verifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Verifier{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  client,
		cache:   make(map[string]cacheEntry),
	}
}

func (v *Verifier) User(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, fmt.Errorf("missing bearer token")
	}
	now := time.Now()
	v.mu.Lock()
	if e, ok := v.cache[token]; ok && now.Before(e.expiresAt) {
		v.mu.Unlock()
		return e.user, nil
	}
	v.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.baseURL+"/auth/v1/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("apikey", v.apiKey)
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("supabase auth: status %d", resp.StatusCode)
	}
	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	if user.ID == "" {
		return nil, fmt.Errorf("supabase auth: no user id")
	}
	v.mu.Lock()
	if len(v.cache) > 1000 {
		v.cache = make(map[string]cacheEntry)
	}
	v.cache[token] = cacheEntry{user: &user, expiresAt: now.Add(30 * time.Second)}
	v.mu.Unlock()
	return &user, nil
}
