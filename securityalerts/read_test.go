package securityalerts_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivy/almanac/github"

	"github.com/technicalpickles/brined-almanac/internal/fakeapi"
	"github.com/technicalpickles/brined-almanac/securityalerts"
)

const token = "alerts-token"

func client(api *fakeapi.Api) *securityalerts.Client {
	return securityalerts.NewClient(api.Origin, token)
}

type obj = map[string]any

// repoApi answers for o/r: a public repository with Dependabot on, one open Dependabot alert
// and one open secret.
func repoApi(t *testing.T) *fakeapi.Api {
	t.Helper()
	api := fakeapi.Serve(t)
	api.Route("/repos/o/r", obj{"private": false})
	api.Status("/repos/o/r/vulnerability-alerts", 204)
	api.Route("/repos/o/r/dependabot/alerts?state=open&per_page=100", []any{fixture(t, "dependabot_alert.json")})
	api.Route("/repos/o/r/secret-scanning/alerts?state=open&per_page=100", []any{fixture(t, "secret_alert.json")})
	return api
}

func TestAnEmpty204IsOnWhereGithubGetFailsDecodingIt(t *testing.T) {
	api := repoApi(t)
	c := client(api)

	_, err := c.Github.Get(context.Background(), "/repos/o/r/vulnerability-alerts")
	var refusal *github.Refusal
	if err == nil || errors.As(err, &refusal) {
		t.Fatalf("Github.Get now reads a 204: use it and drop Client.Status; got %v", err)
	}

	if err := c.Status(context.Background(), "/repos/o/r/vulnerability-alerts"); err != nil {
		t.Errorf("Status failed a 204: %v", err)
	}
}

func TestStatusKeepsTheRefusalAndItsStatus(t *testing.T) {
	api := fakeapi.Serve(t)
	api.Status("/gone", 404)
	api.Status("/denied", 403)
	api.Status("/down", 502)
	c := client(api)
	for path, want := range map[string]int{"/gone": 404, "/denied": 403} {
		err := c.Status(context.Background(), path)
		var refusal *github.Refusal
		if !errors.As(err, &refusal) || refusal.Status != want {
			t.Errorf("%s: %v", path, err)
		}
	}
	err := c.Status(context.Background(), "/down")
	var refusal *github.Refusal
	if err == nil || errors.As(err, &refusal) {
		t.Errorf("a server error is not a refusal: %v", err)
	}
}

func TestGithubReadsKeepA404And403AsRefusals(t *testing.T) {
	api := fakeapi.Serve(t)
	api.Status("/denied", 403)
	for path, want := range map[string]int{"/missing": 404, "/denied": 403} {
		_, err := client(api).Github.Get(context.Background(), path)
		var refusal *github.Refusal
		if !errors.As(err, &refusal) || refusal.Status != want {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestReadRepoReadsBothSourcesWithOneToken(t *testing.T) {
	api := repoApi(t)
	got, err := securityalerts.ReadRepo(context.Background(), client(api), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if got.Private || !got.Dependabot.On || !got.Secrets.On || len(got.Dependabot.Alerts) != 1 || len(got.Secrets.Alerts) != 1 {
		t.Fatalf("%+v", got)
	}
	if got.Dependabot.Alerts[0].Key() != "dependabot:37" || got.Secrets.Alerts[0].Key() != "secret:4" {
		t.Errorf("%+v", got)
	}
	want := []string{
		"/repos/o/r",
		"/repos/o/r/vulnerability-alerts",
		"/repos/o/r/dependabot/alerts?state=open&per_page=100",
		"/repos/o/r/secret-scanning/alerts?state=open&per_page=100",
	}
	if targets := api.Targets(); !slices.Equal(targets, want) {
		t.Errorf("requests %v", targets)
	}
	for _, request := range api.Requests() {
		if request.Auth != "Bearer "+token {
			t.Errorf("%s was sent with %q", request.Target, request.Auth)
		}
	}
}

func TestReadRepoWithDependabotOffNeverListsItsAlerts(t *testing.T) {
	api := repoApi(t)
	api.Status("/repos/o/r/vulnerability-alerts", 404)
	got, err := securityalerts.ReadRepo(context.Background(), client(api), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if got.Dependabot.On || !got.Secrets.On {
		t.Errorf("%+v", got)
	}
	for _, target := range api.Targets() {
		if strings.Contains(target, "dependabot/alerts") {
			t.Errorf("listed %s", target)
		}
	}
}

func TestReadRepoWithSecretScanningOffIsOff(t *testing.T) {
	api := repoApi(t)
	api.Status("/repos/o/r/secret-scanning/alerts?state=open&per_page=100", 404)
	got, err := securityalerts.ReadRepo(context.Background(), client(api), "o/r")
	if err != nil || got.Secrets.On || !got.Dependabot.On {
		t.Errorf("%+v, err %v", got, err)
	}
}

func TestReadRepoWithA403OnTheSwitchFails(t *testing.T) {
	api := repoApi(t)
	api.Status("/repos/o/r/vulnerability-alerts", 403)
	_, err := securityalerts.ReadRepo(context.Background(), client(api), "o/r")
	if err == nil || !strings.Contains(err.Error(), "o/r") || !strings.Contains(err.Error(), "403") {
		t.Errorf("%v", err)
	}
}

func TestReadRepoWithA401OnSecretScanningFails(t *testing.T) {
	api := repoApi(t)
	api.Status("/repos/o/r/secret-scanning/alerts?state=open&per_page=100", 401)
	_, err := securityalerts.ReadRepo(context.Background(), client(api), "o/r")
	if err == nil || !strings.Contains(err.Error(), "o/r") || !strings.Contains(err.Error(), "401") {
		t.Errorf("%v", err)
	}
}

func TestReadRepoOfAMissingRepositoryPointsAtTheTiersFiles(t *testing.T) {
	_, err := securityalerts.ReadRepo(context.Background(), client(repoApi(t)), "o/gone")
	if err == nil || !strings.Contains(err.Error(), "o/gone was not found on GitHub: check tiers.toml and local/tiers.toml") {
		t.Errorf("%v", err)
	}
}

func TestReadRepoWithoutAPrivateFlagFails(t *testing.T) {
	for name, answer := range map[string]any{"missing": obj{}, "null": obj{"private": nil}, "text": obj{"private": "no"}, "list": []any{}} {
		api := repoApi(t)
		api.Route("/repos/o/r", answer)
		_, err := securityalerts.ReadRepo(context.Background(), client(api), "o/r")
		if err == nil || err.Error() != "o/r has no private flag in the response" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReadRepoWithAnUnreadableAlertFailsNamingTheRepository(t *testing.T) {
	api := repoApi(t)
	api.Route("/repos/o/r/dependabot/alerts?state=open&per_page=100", []any{obj{"number": 1}})
	_, err := securityalerts.ReadRepo(context.Background(), client(api), "o/r")
	if err == nil || !strings.HasPrefix(err.Error(), "o/r: ") {
		t.Errorf("%v", err)
	}
}

func TestReadRepoOfAPrivateRepository(t *testing.T) {
	api := repoApi(t)
	api.Route("/repos/o/r", obj{"private": true})
	got, err := securityalerts.ReadRepo(context.Background(), client(api), "o/r")
	if err != nil || !got.Private {
		t.Errorf("%+v, err %v", got, err)
	}
}
