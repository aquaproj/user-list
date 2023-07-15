package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/google/go-github/v53/github"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

func main() {
	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.InfoLevel).With().Str("program", "list-aqua-users").Logger()

	if err := core(logger); err != nil {
		log.Fatal().Err(err).Send()
	}
}

func core(logger zerolog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	gh := NewGitHub(ctx)
	// query := "-user:suzuki-shunsuke -org:aquaproj aquaproj"
	query := "aquaproj/aqua-registry"
	excludedOwners := map[string]struct{}{}

	resultMap, err := gh.SearchRepos(ctx, logger, query, excludedOwners)

	results := make([]*Result, 0, len(resultMap))
	for _, result := range resultMap {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Star > results[j].Star {
			return true
		}
		if results[i].Star < results[j].Star {
			return false
		}
		return results[i].Repo < results[j].Repo
	})

	if runID := os.Getenv("GITHUB_RUN_ID"); runID != "" {
		fmt.Printf(`_The last updated time: [%s](%s/%s/actions/runs/%s)_

Repository | :star: The number of GitHub stars
--- | ---
`,
			time.Now().Format(time.RFC3339),
			os.Getenv("GITHUB_SERVER_URL"),
			os.Getenv("GITHUB_REPOSITORY"),
			runID)
	} else {
		fmt.Printf(`_The last updated time: %s_

Repository | :star: The number of GitHub stars
--- | ---
`, time.Now().Format(time.RFC3339))
	}

	for _, result := range results {
		fmt.Printf("[%s](https://github.com/%s) | [%d](https://github.com/%s/stargazers)\n", result.Repo, result.Repo, result.Star, result.Repo)
	}
	fmt.Println("")

	if err != nil {
		return err
	}
	return nil
}

type GitHub struct {
	search   *github.SearchService
	v4Client *githubv4.Client
}

type Result struct {
	Repo  string
	Star  int
	Owner string
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

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck
	}
}

const bufferTime = 5 * time.Second

func (gh *GitHub) SearchRepos(ctx context.Context, logger zerolog.Logger, query string, excludedOwners map[string]struct{}) (map[string]*Result, error) { //nolint:funlen,gocognit,cyclop
	opts := &github.SearchOptions{
		ListOptions: github.ListOptions{
			PerPage: 100, //nolint:gomnd
		},
	}
	resultMap := map[string]*Result{}
	for i := 0; i < 30; i++ {
		// API rate limit
		// https://docs.github.com/en/rest/search/search?apiVersion=2022-11-28#considerations-for-code-search
		// This endpoint requires you to authenticate and limits you to 10 requests per minute.
		result, _, err := gh.search.Code(ctx, query, opts)
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
				logger.Warn().Err(err).Msg("get a repository by GraphQL API")
				continue
			}
			logger.Info().Str("repo", repoFullName).Int("star", repo.Star).Send()
			resultMap[repoFullName] = &Result{
				Repo: repoFullName,
				Star: repo.Star,
			}
		}
		if len(result.CodeResults) < opts.PerPage {
			break
		}
		opts.ListOptions.Page++
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
