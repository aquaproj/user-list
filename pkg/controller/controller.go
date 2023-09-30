package controller

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/rs/zerolog"
)

type Controller struct{}

func New() *Controller {
	return &Controller{}
}

func (c *Controller) Run(ctx context.Context, logger zerolog.Logger) error {
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
		fmt.Printf(`_The last update: [%s](%s/%s/actions/runs/%s)_

The number of repositories: %d

Repository | :star: The number of GitHub stars
--- | ---
`,
			time.Now().Format(time.RFC3339),
			os.Getenv("GITHUB_SERVER_URL"),
			os.Getenv("GITHUB_REPOSITORY"),
			runID,
			len(results),
		)
	} else {
		fmt.Printf(`_The last update: %s_

The number of repositories: %d

Repository | :star: The number of GitHub stars
--- | ---
`, time.Now().Format(time.RFC3339), len(results))
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
