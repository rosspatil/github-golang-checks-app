package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v31/github"
)

func main() {
	g := gin.New()

	g.POST("/api/github/hook", githubPRHook)

	g.Run(":8080")
}

func githubPRHook(c *gin.Context) {
	actionOn := c.Request.Header.Get("X-Github-Event")

	it := createToken()
	fmt.Println(actionOn)
	switch actionOn {
	case "check_suite":
		createCheckSuite(c, it)
	case "check_run":
		startCheck(c, it)
	}
}

func createCheckSuite(c *gin.Context, it *github.InstallationToken) {
	ev := &github.CheckSuiteEvent{}
	err := c.BindJSON(ev)
	if err != nil {
		log.Fatal(err)
	}
	if ev.GetAction() == "completed" {
		return
	}
	repoOwner := ev.Repo.GetOwner().GetLogin()
	repoName := ev.Repo.GetName()
	opts := github.CheckRun{}
	opts.Name = github.String("Golang Code linter")
	opts.StartedAt = &github.Timestamp{time.Now()}
	opts.HeadSHA = ev.CheckSuite.HeadSHA
	ba, _ := json.Marshal(opts)
	req, _ := http.NewRequest("POST", "https://api.github.com/repos/"+repoOwner+"/"+repoName+"/check-runs", bytes.NewBuffer(ba))
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.antiope-preview+json")
	req.Header.Set("Authorization", "Bearer "+*it.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
}

func createToken() *github.InstallationToken {
	t := time.Now()
	claims := jwt.MapClaims{}
	claims["iat"] = t.Unix()
	claims["exp"] = t.Add(time.Minute * 1).Unix()
	claims["iss"] = 67112
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	ba, err := ioutil.ReadFile("key.pem")
	k, err := jwt.ParseRSAPrivateKeyFromPEM(ba)
	if err != nil {
		log.Fatal(err)
	}
	str, err := token.SignedString(k)
	if err != nil {
		log.Fatal(err)
	}

	req, _ := http.NewRequest("POST", "https://api.github.com/app/installations/9717368/access_tokens", nil)

	req.Header.Set("Accept", "application/vnd.github.machine-man-preview+json")
	req.Header.Set("Authorization", "Bearer "+str)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	ba, _ = ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		log.Fatal(string(ba))
	}
	it := github.InstallationToken{}
	err = json.Unmarshal(ba, &it)
	if err != nil {
		log.Fatal(err)
	}
	return &it
}

func startCheck(c *gin.Context, it *github.InstallationToken) {
	ev := &github.CheckRunEvent{}
	err := c.BindJSON(ev)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(ev.CheckRun.GetStatus())
	if ev.CheckRun.GetStatus() != "queued" {
		return
	}
	repoName := ev.Repo.GetName()
	repoOwner := ev.Repo.GetOwner().GetLogin()
	id := strconv.FormatInt(ev.CheckRun.GetID(), 10)
	go func() {
		opts := &github.CheckRun{
			Name:    ev.GetCheckRun().Name,
			ID:      ev.GetCheckRun().ID,
			HeadSHA: ev.GetCheckRun().HeadSHA,
		}

		opts.Status = github.String("in_progress")
		setStatus(opts, repoName, repoOwner, id, it)
		err := gitCheckout(ev.GetCheckRun().GetHeadSHA(), ev.Repo.GetCloneURL(), ev.CheckRun.CheckSuite.GetHeadBranch(), it.GetToken())
		if err != nil {
			log.Println(err)
			return
		}
		err = codeCheck(opts, ev.Repo.GetName(), it.GetToken())
		if err != nil {
			opts.Output = &github.CheckRunOutput{
				Text:    github.String("Pipeline failed with " + err.Error()),
				Title:   github.String("Code check failed"),
				Summary: github.String("Code Linter is throwing error"),
			}
			opts.CompletedAt = &github.Timestamp{time.Now()}
			opts.Status = github.String("completed")
			opts.Conclusion = github.String("failure")
			setStatus(opts, repoName, repoOwner, id, it)

		} else {
			opts.Output = &github.CheckRunOutput{
				Text:    github.String("No Code Smell found"),
				Title:   github.String("Code check is passed"),
				Summary: github.String("All Good"),
			}
			opts.CompletedAt = &github.Timestamp{time.Now()}
			opts.Status = github.String("completed")
			opts.Conclusion = github.String("success")
			setStatus(opts, repoName, repoOwner, id, it)
		}
	}()
}

func setStatus(ev *github.CheckRun, repoName, repoOwner, id string, it *github.InstallationToken) {
	ba, _ := json.Marshal(ev)

	req, _ := http.NewRequest(http.MethodPatch, "https://api.github.com/repos/"+repoOwner+"/"+repoName+"/check-runs/"+id, bytes.NewBuffer(ba))
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Authorization", "Bearer "+*it.Token)
	req.Header.Set("Accept", "application/vnd.github.antiope-preview+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
}

func gitCheckout(id, url, branch, token string) error {
	gitURL := strings.TrimPrefix(url, "https://")
	gitURL = "https://" + token + "@" + gitURL
	args := []string{"clone", "-b", branch, gitURL}
	cmd := exec.Command("git", args...)
	errBuf := &bytes.Buffer{}
	cmd.Stderr = errBuf
	path := "/tmp/checks/" + id
	os.MkdirAll(path, os.ModePerm)
	cmd.Dir = path
	err := cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func codeCheck(checkRun *github.CheckRun, repoName, token string) error {
	fmt.Println("checking code smells")
	args := []string{"--color=always", "--exclude-use-default=false", "--print-linter-name", "-E=govet", "-E=golint", "-E=asciicheck", "-E=bodyclose", "-E=dupl", "-E=gocognit", "-E=gocyclo", "-E=goerr113", "-E=deadcode", "-E=gomnd", "-E=gosec", "-E=misspell", "-E=nestif", "-E=rowserrcheck", "-E=unconvert", "-E=unparam", "-E=whitespace", "-E=goconst", "run", "./..."}
	path := "/tmp/checks/" + checkRun.GetHeadSHA() + "/" + repoName

	cmd := exec.Command("golangci-lint", args...)
	errBuf := &bytes.Buffer{}
	out := &bytes.Buffer{}
	cmd.Stderr = errBuf
	cmd.Stdout = out
	cmd.Dir = path
	err := cmd.Run()
	if errBuf.String() != "" {
		return errors.New(errBuf.String())
	}
	if out.String() != "" {
		return errors.New("```" + out.String() + "```")
	}

	if err != nil {
		return err
	}
	fmt.Println("checked code smells")

	return nil
}
