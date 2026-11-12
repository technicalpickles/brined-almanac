package securityalerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ivy/almanac/diag"
	"github.com/ivy/almanac/github"
	"github.com/ivy/almanac/hostile"
)

// Coverage is one alert source of a repository: its open alerts when the source is on, and
// nothing when it is off.
type Coverage struct {
	On     bool
	Alerts []Alert
}

// RepoRead is what one repository's alert sources amount to.
type RepoRead struct {
	Private    bool
	Dependabot Coverage
	Secrets    Coverage
}

// Client reads GitHub for the alerts workflow. Github does every read it can. Whether
// Dependabot alerts are on is a status-only call of Client's own, because the endpoint answers
// 204 with no body and Github.Get fails decoding it.
type Client struct {
	Github *github.Client

	base  string
	token github.Token
	http  *http.Client
}

// NewClient is a client of the API at baseURL, such as https://api.github.com, that
// authenticates with token. Both its Github and its own call use the same pair.
func NewClient(baseURL string, token github.Token, options ...github.Option) *Client {
	return &Client{
		Github: github.New(baseURL, token, options...),
		base:   baseURL,
		token:  token,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Status makes a GET of path and says only how it was answered: nil for any 2xx, whatever the
// body, and otherwise an error that wraps a *github.Refusal for a refusal, as Github's reads do.
func (c *Client) Status(ctx context.Context, path string) error {
	action := "GET " + path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return diag.Unreachable(action, err.Error())
	}
	request.Header.Set("Authorization", "Bearer "+string(c.token))
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "almanac")
	response, err := c.http.Do(request)
	if err != nil {
		return diag.Unreachable(action, err.Error())
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode >= 500:
		return diag.Unreachable(action, "GitHub answered "+response.Status)
	default:
		refused := diag.Refused(action, response.StatusCode)
		refused.Cause = &github.Refusal{Status: response.StatusCode}
		return refused
	}
}

func failed(repo string, err error) error {
	return fmt.Errorf("%s: %v", repo, err)
}

func isNotFound(err error) bool {
	var refusal *github.Refusal
	return errors.As(err, &refusal) && refusal.Status == http.StatusNotFound
}

// AlertsSwitch is whether Dependabot alerts are on, from the answer to asking. Only a 404 means
// off: Dependabot answers 403 for a missing permission too.
func AlertsSwitch(answer error, repo string) (bool, error) {
	switch {
	case answer == nil:
		return true, nil
	case isNotFound(answer):
		return false, nil
	default:
		return false, failed(repo, answer)
	}
}

// SecretAlerts is the secret scanning alerts of repo, from the answer to listing them. A 404
// means off only because the caller already proved the repository exists.
func SecretAlerts(items []json.RawMessage, answer error, repo string) (Coverage, error) {
	switch {
	case answer == nil:
		alerts, err := parsed(items, ParseSecret, repo)
		return Coverage{On: true, Alerts: alerts}, err
	case isNotFound(answer):
		return Coverage{}, nil
	default:
		return Coverage{}, failed(repo, answer)
	}
}

// RepoMissing is the error for a repository that could not be read at all. A 404 there points
// at the tiers files.
func RepoMissing(answer error, repo string) error {
	if isNotFound(answer) {
		return fmt.Errorf("%s was not found on GitHub: check tiers.toml and local/tiers.toml", repo)
	}
	return failed(repo, answer)
}

func parsed(items []json.RawMessage, parse func(json.RawMessage) (Alert, error), repo string) ([]Alert, error) {
	alerts := make([]Alert, 0, len(items))
	for _, item := range items {
		alert, err := parse(item)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", repo, err)
		}
		alerts = append(alerts, alert)
	}
	return alerts, nil
}

// ReadRepo reads one repository: that it exists and is private or not, whether Dependabot
// alerts are on and the open ones if so, and the open secret scanning alerts if that is on.
func ReadRepo(ctx context.Context, c *Client, repo string) (RepoRead, error) {
	found, err := c.Github.Get(ctx, "/repos/"+repo)
	if err != nil {
		return RepoRead{}, RepoMissing(err, repo)
	}
	var flag struct {
		Private *bool `json:"private"`
	}
	if hostile.DecodeClean(found, &flag) != nil || flag.Private == nil {
		return RepoRead{}, fmt.Errorf("%s has no private flag in the response", repo)
	}

	on, err := AlertsSwitch(c.Status(ctx, "/repos/"+repo+"/vulnerability-alerts"), repo)
	if err != nil {
		return RepoRead{}, err
	}
	dependabot := Coverage{}
	if on {
		items, err := c.Github.GetAll(ctx, "/repos/"+repo+"/dependabot/alerts?state=open")
		if err != nil {
			return RepoRead{}, failed(repo, err)
		}
		alerts, err := parsed(plain(items), ParseDependabot, repo)
		if err != nil {
			return RepoRead{}, err
		}
		dependabot = Coverage{On: true, Alerts: alerts}
	}

	items, err := c.Github.GetAll(ctx, "/repos/"+repo+"/secret-scanning/alerts?state=open")
	secrets, err := SecretAlerts(plain(items), err, repo)
	if err != nil {
		return RepoRead{}, err
	}
	return RepoRead{Private: *flag.Private, Dependabot: dependabot, Secrets: secrets}, nil
}
