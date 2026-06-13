package cli

import (
	"errors"

	"github.com/tamnd/githubdocs-cli/githubdocs"
)

func isNotFound(err error) bool {
	return errors.Is(err, githubdocs.ErrNotFound)
}
