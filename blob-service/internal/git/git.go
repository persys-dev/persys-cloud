package git

import (
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	myhttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	urlHelper "net/url"
	"os"
	"strings"
)

func url2Dir(url string) (route string) {

	//defer errorhandler.ErrHandler()

	mix, err := urlHelper.Parse(url)
	if err != nil {

	}
	//CheckIfError(err)

	path := mix.Path

	route = path[:strings.IndexByte(path, '.')]

	return route
}

func Gits(url string, private bool, token string) (*object.Commit, string, error) {
	var action string

	directory := "/artifacts/git" + url2Dir(url)

	fs, _ := os.Stat(directory)

	if fs == nil {
		action = "Clone"
	}
	if fs == nil && private == true {
		action = "CloneWithAuth"
	}
	if fs != nil && private == false {
		//Info("Fetching repository: %v", request.GitURL)
		action = "Fetch"
	}
	if fs != nil && private == true {
		action = "FetchWithAuth"

	}

	switch action {

	case "Clone":
		//fmt.Println(url)
		r, err := git.PlainClone(directory, false, &git.CloneOptions{
			URL:               url,
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		})

		ref, err := r.Head()
		commit, err := r.CommitObject(ref.Hash())

		if err != nil {
			return nil, "", err
		}

		return commit, directory, nil

	case "Fetch":
		open, err := git.PlainOpen(directory)
		//CheckIfError(err)
		w, err := open.Worktree()
		err = w.Pull(&git.PullOptions{RemoteName: "origin"})

		if err != nil && err.Error() == "already up-to-date" {
			ref, err := open.Head()
			if err != nil {

			}
			commit, err := open.CommitObject(ref.Hash())
			return commit, directory, nil
		}
		if err != nil {
			return nil, "", err
		}

	case "FetchWithAuth":
		open, err := git.PlainOpen(directory)
		//CheckIfError(err)
		w, err := open.Worktree()
		err = w.Pull(&git.PullOptions{RemoteName: "origin", Auth: &myhttp.BasicAuth{
			Username: "abc123",
			Password: token,
		}})

		if err != nil {
			return nil, "", err
		}
		ref, err := open.Head()
		commit, err := open.CommitObject(ref.Hash())

		return commit, directory, nil

	case "Rollback":
		print("hello")

	case "CloneWithAuth":
		//token := req.AccessToken
		r, err := git.PlainClone(directory, false, &git.CloneOptions{
			URL: url,
			Auth: &myhttp.BasicAuth{
				Username: "abc123", // yes, this can be anything except an empty string
				Password: token,
			},
			RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		})
		if err != nil {
			return nil, "", err
		}
		ref, err := r.Head()
		commit, err := r.CommitObject(ref.Hash())
		//CheckIfError(err)
		//id := idGen()
		return commit, directory, nil
	}
	return nil, "nothing", nil
}
