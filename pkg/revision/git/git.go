package git

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	openfunction "github.com/openfunction/apis/core/v1beta1"
	"github.com/openfunction/revision-controller/pkg/constants"
	"github.com/openfunction/revision-controller/pkg/revision"
	"github.com/openfunction/revision-controller/pkg/revision/git/provider"
	"github.com/openfunction/revision-controller/pkg/revision/git/provider/gitee"
	"github.com/openfunction/revision-controller/pkg/revision/git/provider/github"
	"github.com/openfunction/revision-controller/pkg/revision/git/provider/gitlab"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	password = "password"

	gitProviderGithub = "github"
	gitProviderGitlab = "gitlab"
	gitProviderGitee  = "gitee"
)

type Revision struct {
	client.Client
	log    logr.Logger
	fn     *openfunction.Function
	config *Config

	gitConfig   *provider.GitConfig
	gitProvider provider.GitProvider

	stopCh chan os.Signal
}

type Config struct {
	RepoType        string
	PollingInterval time.Duration
}

func NewRevision(c client.Client, fn *openfunction.Function, revisionType string, config map[string]string) (revision.Revision, error) {
	r := &Revision{
		Client: c,
		log:    ctrl.Log.WithName("Revision").WithValues("Function", fn.Namespace+"/"+fn.Name, "Type", revisionType),
		fn:     fn,
		stopCh: make(chan os.Signal),
	}
	signal.Notify(r.stopCh, os.Interrupt, syscall.SIGTERM)

	var err error
	r.config, err = r.getRevisionConfig(config)
	if err != nil {
		return nil, err
	}

	r.gitConfig, err = r.getGitConfig(config)
	if err != nil {
		return nil, err
	}

	r.gitProvider, err = newProvider(r.config.RepoType, r.gitConfig)
	return r, err
}

func (r *Revision) Start() {
	go func() {
		compare := func() {
			head, err := r.gitProvider.GetHead()
			if err != nil {
				r.log.Error(err, "get git repository head error")
				return
			}

			currentHead, err := r.getCurrentHead()
			if currentHead == head {
				r.log.V(1).Info("source code has no change")
				return
			}

			if currentHead == "" {
				r.log.V(1).Info("function was just created")
				return
			}

			r.log.Info("source code changed, rebuild function")
			// The source code had changed, rebuild the function.
			if err := r.updateFunctionStatus(head); err != nil {
				r.log.Error(err, "update function status error")
				return
			}

			return
		}

		for {
			select {
			case <-r.stopCh:
				r.log.Info("revision stopped")
				return
			default:
			}

			compare()
			time.Sleep(r.config.PollingInterval)
		}
	}()

	r.log.Info("revision started")
}

func (r *Revision) Update(config map[string]string) error {
	revisionConfig, err := r.getRevisionConfig(config)
	if err != nil {
		return err
	}

	gitConfig, err := r.getGitConfig(config)
	if err != nil {
		return err
	}

	if revisionConfig.RepoType != r.config.RepoType ||
		!reflect.DeepEqual(r.gitConfig, gitConfig) {
		r.log.Info("update git provider")
		gp, err := newProvider(revisionConfig.RepoType, gitConfig)
		if err != nil {
			return err
		}

		r.gitProvider = gp
		r.gitConfig = gitConfig
	}

	r.config = revisionConfig
	return nil
}

func (r *Revision) Stop() {
	close(r.stopCh)
	signal.Stop(r.stopCh)
}

func (r *Revision) getRevisionConfig(config map[string]string) (*Config, error) {
	interval := constants.DefaultPollingInterval
	str := config[constants.PollingInterval]
	if str != "" {
		var err error
		interval, err = time.ParseDuration(str)
		if err != nil {
			return nil, err
		}
	}

	revisionConfig := &Config{
		RepoType:        config[constants.RepoType],
		PollingInterval: interval,
	}

	if revisionConfig.RepoType == "" {
		revisionConfig.RepoType = gitProviderGithub
	}

	return revisionConfig, nil
}

func (r *Revision) getGitConfig(config map[string]string) (*provider.GitConfig, error) {
	function, err := r.getFunction()
	if err != nil {
		return nil, err
	}

	gitConfig := &provider.GitConfig{}
	gitConfig.URL = function.Spec.Build.SrcRepo.Url
	gitConfig.Branch = function.Spec.Build.SrcRepo.Revision
	gitConfig.BaseURL = config[constants.BaseURL]
	gitConfig.AuthType = config[constants.AuthType]
	gitConfig.Project = config[constants.Project]

	if function.Spec.Build.SrcRepo.Credentials == nil {
		return nil, fmt.Errorf("%s", "the source credential must be set")
	}

	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      function.Spec.Build.SrcRepo.Credentials.Name,
			Namespace: function.Namespace,
		},
	}

	if err := r.Get(context.Background(), client.ObjectKeyFromObject(secret), secret); err != nil {
		return nil, err
	}
	gitConfig.Password = string(secret.Data[password])

	return gitConfig, nil
}

func (r *Revision) getCurrentHead() (string, error) {
	function, err := r.getFunction()
	if err != nil {
		return "", err
	}

	if function.Status.Sources == nil {
		return "", nil
	}

	for _, source := range function.Status.Sources {
		if source.Name == "default" && source.Git != nil {
			return source.Git.CommitSha, nil
		}
	}

	return "", nil
}

func (r *Revision) updateFunctionStatus(head string) error {
	function, err := r.getFunction()
	if err != nil {
		return err
	}

	function.Status.Build = nil
	function.Status.Sources = nil
	function.Status.Sources = append(function.Status.Sources, openfunction.SourceResult{
		Name: "default",
		Git: &openfunction.GitSourceResult{
			CommitSha: head,
		},
	})

	return r.Status().Update(context.Background(), function)
}

func (r *Revision) getFunction() (*openfunction.Function, error) {
	fn := &openfunction.Function{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.fn.Name,
			Namespace: r.fn.Namespace,
		},
	}

	if err := r.Get(context.Background(), client.ObjectKeyFromObject(fn), fn); err != nil {
		return nil, err
	}

	return fn, nil
}

func newProvider(gitProvider string, config *provider.GitConfig) (provider.GitProvider, error) {
	var err error
	var gp provider.GitProvider
	switch gitProvider {
	case gitProviderGithub:
		gp, err = github.NewProvider(config)
	case gitProviderGitlab:
		gp, err = gitlab.NewProvider(config)
	case gitProviderGitee:
		gp, err = gitee.NewProvider(config)
	default:
		return nil, fmt.Errorf("unspport git provider, %s", gitProvider)
	}

	return gp, err
}
