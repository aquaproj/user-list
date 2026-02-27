package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/go-github/v83/github"
	"github.com/rs/zerolog"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

type Result struct {
	Repo  string
	Star  int
	Owner string
}

type GitHub struct {
	search   *github.SearchService
	v4Client *githubv4.Client
}

func NewGitHub(ctx context.Context) *GitHub {
	httpClient := getHTTPClientForGitHub(ctx, getGitHubToken())
	return &GitHub{
		search:   github.NewClient(httpClient).Search,
		v4Client: githubv4.NewClient(httpClient),
	}
}

func getGitHubToken() string {
	return os.Getenv("GITHUB_TOKEN")
}

func getHTTPClientForGitHub(ctx context.Context, token string) *http.Client {
	if token == "" {
		return http.DefaultClient
	}
	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	))
}

func (gh *GitHub) SearchRepos(ctx context.Context, logger zerolog.Logger, query string, excludedOwners map[string]struct{}) (map[string]*Result, error) { //nolint:funlen,gocognit,cyclop
	opts := &github.SearchOptions{
		ListOptions: github.ListOptions{
			PerPage: 100, //nolint:mnd
		},
	}
	resultMap := map[string]*Result{}
	for range 50 {
		// API rate limit
		// https://docs.github.com/en/rest/search/search?apiVersion=2022-11-28#considerations-for-code-search
		// This endpoint requires you to authenticate and limits you to 10 requests per minute.
		result, resp, err := gh.search.Code(ctx, query, opts)
		if err != nil { //nolint:nestif
			rateLimitError := &github.RateLimitError{}
			if ok := errors.As(err, &rateLimitError); ok {
				now := time.Now()
				endTime := rateLimitError.Rate.Reset.GetTime().Add(bufferTime)
				duration := endTime.Sub(now)
				logger.Info().Err(err).Msgf("API rate limit exceeded. Wait until %v (%v)", endTime, duration)
				if err := wait(ctx, duration); err != nil {
					return resultMap, err
				}
				continue
			}
			abuseRateLimitError := &github.AbuseRateLimitError{}
			if ok := errors.As(err, &abuseRateLimitError); ok {
				if abuseRateLimitError.RetryAfter == nil {
					return resultMap, err //nolint:wrapcheck
				}
				now := time.Now()
				duration := *abuseRateLimitError.RetryAfter + bufferTime
				logger.Info().Err(err).Msgf("Secondary API rate limit exceeded. Wait until %v (%v)", now.Add(duration), duration)
				if err := wait(ctx, duration); err != nil {
					return resultMap, err
				}
				continue
			}
			return resultMap, err //nolint:wrapcheck
		}
		logger.Info().Msgf("total: %d\n", result.GetTotal())
		for _, codeResult := range result.CodeResults {
			owner := codeResult.Repository.GetOwner().GetLogin()
			if _, ok := excludedOwners[owner]; ok {
				continue
			}
			repoFullName := codeResult.Repository.GetFullName()
			if _, ok := resultMap[repoFullName]; ok {
				continue
			}
			repoName := codeResult.Repository.GetName()
			repo, err := gh.GetRepo(ctx, owner, repoName)
			if err != nil {
				// logger.Warn().Err(err).Msg("get a repository by GraphQL API")
				return resultMap, fmt.Errorf("get a repository by GraphQL API: %w", err)
			}
			logger.Info().Str("repo", repoFullName).Int("star", repo.Star).Send()
			resultMap[repoFullName] = &Result{
				Repo: repoFullName,
				Star: repo.Star,
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return resultMap, nil
}

type Repo struct {
	Star int
}

func (gh *GitHub) GetRepo(ctx context.Context, repoOwner, repoName string) (Repo, error) {
	/*
	  repository(owner: "suzuki-shunsuke", name: "tfcmt") {
	    stargazerCount
	  }
	*/
	var q struct {
		Repository struct {
			StargazerCount githubv4.Int
		} `graphql:"repository(owner: $owner, name: $name)"`
	}
	variables := map[string]interface{}{
		"owner": githubv4.String(repoOwner),
		"name":  githubv4.String(repoName),
	}

	if err := gh.v4Client.Query(ctx, &q, variables); err != nil {
		return Repo{}, fmt.Errorf("get a repository by GitHub GraphQL API: %w", err)
	}
	return Repo{
		Star: int(q.Repository.StargazerCount),
	}, nil
}
