package helm

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	yaml "gopkg.in/yaml.v2"

	"github.com/covexo/devspace/pkg/util/fsutil"
	"github.com/covexo/devspace/pkg/util/log"

	helminstaller "k8s.io/helm/cmd/helm/installer"
	"k8s.io/helm/pkg/downloader"
	"k8s.io/helm/pkg/getter"
	"k8s.io/helm/pkg/kube"
	"k8s.io/helm/pkg/repo"

	"k8s.io/client-go/kubernetes"

	"github.com/covexo/devspace/pkg/devspace/clients/kubectl"
	"github.com/covexo/devspace/pkg/devspace/config"
	"github.com/covexo/devspace/pkg/devspace/config/v1"
	homedir "github.com/mitchellh/go-homedir"
	k8sv1 "k8s.io/api/core/v1"
	k8sv1beta1 "k8s.io/api/rbac/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	helmchartutil "k8s.io/helm/pkg/chartutil"
	helmdownloader "k8s.io/helm/pkg/downloader"
	k8shelm "k8s.io/helm/pkg/helm"
	helmenvironment "k8s.io/helm/pkg/helm/environment"
	"k8s.io/helm/pkg/helm/helmpath"
	"k8s.io/helm/pkg/helm/portforwarder"
	hapi_release5 "k8s.io/helm/pkg/proto/hapi/release"
	rls "k8s.io/helm/pkg/proto/hapi/services"
	helmstoragedriver "k8s.io/helm/pkg/storage/driver"
)

// HelmClientWrapper holds the necessary information for helm
type HelmClientWrapper struct {
	Client   *k8shelm.Client
	Settings *helmenvironment.EnvSettings
	kubectl  *kubernetes.Clientset
}

const tillerServiceAccountName = "devspace-tiller"
const tillerRoleName = "devspace-tiller"
const tillerDeploymentName = "tiller-deploy"
const stableRepoCachePath = "repository/cache/stable-index.yaml"
const defaultRepositories = `apiVersion: v1
repositories:
- caFile: ""
  cache: ` + stableRepoCachePath + `
  certFile: ""
  keyFile: ""
  name: stable
  url: https://kubernetes-charts.storage.googleapis.com
`

var privateConfig = &v1.PrivateConfig{}
var defaultPolicyRules = []k8sv1beta1.PolicyRule{
	{
		APIGroups: []string{
			k8sv1beta1.APIGroupAll,
			"extensions",
			"apps",
		},
		Resources: []string{k8sv1beta1.ResourceAll},
		Verbs:     []string{k8sv1beta1.ResourceAll},
	},
}

// NewClient creates a new helm client
func NewClient(kubectlClient *kubernetes.Clientset, upgradeTiller bool) (*HelmClientWrapper, error) {
	config.LoadConfig(privateConfig)

	kubeconfig, err := kubectl.GetClientConfig()

	if err != nil {
		return nil, err
	}

	tillerErr := ensureTiller(kubectlClient, upgradeTiller)

	if tillerErr != nil {
		return nil, tillerErr
	}

	var tunnelErr error
	var tunnel *kube.Tunnel

	tunnelWaitTime := 2 * 60 * time.Second
	tunnelCheckInterval := 5 * time.Second

	log.StartWait("Waiting for tiller portforwarding to become ready")

	for tunnelWaitTime > 0 {
		tunnel, tunnelErr = portforwarder.New(privateConfig.Cluster.TillerNamespace, kubectlClient, kubeconfig)

		if tunnelErr == nil || tunnelWaitTime < 0 {
			break
		}

		tunnelWaitTime = tunnelWaitTime - tunnelCheckInterval
		time.Sleep(tunnelCheckInterval)
	}

	log.StopWait()

	if tunnelErr != nil {
		return nil, tunnelErr
	}

	helmWaitTime := 2 * 60 * time.Second
	helmCheckInterval := 5 * time.Second

	helmOptions := []k8shelm.Option{
		k8shelm.Host("127.0.0.1:" + strconv.Itoa(tunnel.Local)),
		k8shelm.ConnectTimeout(int64(helmCheckInterval)),
	}
	client := k8shelm.NewClient(helmOptions...)
	var tillerError error

	log.StartWait("Waiting for tiller server to become ready")

	for helmWaitTime > 0 {
		_, tillerError = client.ListReleases(k8shelm.ReleaseListLimit(1))

		if tillerError == nil || helmWaitTime < 0 {
			break
		}
	}

	log.StopWait()
	log.Done("Tiller server is ready")

	if tillerError != nil {
		return nil, tillerError
	}

	homeDir, err := homedir.Dir()

	if err != nil {
		return nil, err
	}

	helmHomePath := homeDir + "/.devspace/helm"
	repoPath := helmHomePath + "/repository"
	repoFile := repoPath + "/repositories.yaml"
	stableRepoCachePathAbs := helmHomePath + "/" + stableRepoCachePath

	os.MkdirAll(helmHomePath+"/cache", os.ModePerm)
	os.MkdirAll(repoPath, os.ModePerm)
	os.MkdirAll(filepath.Dir(stableRepoCachePathAbs), os.ModePerm)

	_, repoFileNotFound := os.Stat(repoFile)

	if repoFileNotFound != nil {
		fsutil.WriteToFile([]byte(defaultRepositories), repoFile)
	}

	wrapper := &HelmClientWrapper{
		Client: client,
		Settings: &helmenvironment.EnvSettings{
			Home: helmpath.Home(helmHomePath),
		},
		kubectl: kubectlClient,
	}
	_, stableRepoCacheNotFoundErr := os.Stat(stableRepoCachePathAbs)

	if stableRepoCacheNotFoundErr != nil {
		wrapper.updateRepos()
	}

	return wrapper, nil
}

func ensureTiller(kubectlClient *kubernetes.Clientset, upgrade bool) error {
	tillerSA := &k8sv1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tillerServiceAccountName,
			Namespace: privateConfig.Cluster.TillerNamespace,
		},
	}
	tillerOptions := &helminstaller.Options{
		Namespace:      privateConfig.Cluster.TillerNamespace,
		MaxHistory:     10,
		ImageSpec:      "gcr.io/kubernetes-helm/tiller:v2.9.1",
		ServiceAccount: tillerSA.ObjectMeta.Name,
	}
	_, tillerCheckErr := kubectlClient.ExtensionsV1beta1().Deployments(privateConfig.Cluster.TillerNamespace).Get(tillerDeploymentName, metav1.GetOptions{})

	// Tiller is not there
	if tillerCheckErr != nil {
		log.StartWait("Installing Tiller server")
		defer log.StopWait()

		_, err := kubectlClient.CoreV1().ServiceAccounts(tillerSA.Namespace).Get(tillerSA.Name, metav1.GetOptions{})

		if err != nil {
			_, err := kubectlClient.CoreV1().ServiceAccounts(tillerSA.Namespace).Create(tillerSA)

			if err != nil {
				return err
			}
		}

		err = ensureRoleBinding(kubectlClient, "tiller-config-manager", privateConfig.Cluster.TillerNamespace, privateConfig.Cluster.TillerNamespace, []k8sv1beta1.PolicyRule{
			{
				APIGroups: []string{
					k8sv1beta1.APIGroupAll,
					"extensions",
					"apps",
				},
				Resources: []string{
					"configmaps",
				},
				Verbs: []string{k8sv1beta1.ResourceAll},
			},
		})

		if err != nil {
			return err
		}

		helminstaller.Install(kubectlClient, tillerOptions)

		err = ensureRoleBinding(kubectlClient, tillerRoleName, privateConfig.Release.Namespace, privateConfig.Cluster.TillerNamespace, defaultPolicyRules)

		if err != nil {
			return err
		}

		log.StopWait()

		//Upgrade of tiller is necessary
	} else if upgrade {
		log.StartWait("Upgrading Tiller server")

		tillerOptions.ImageSpec = ""
		err := helminstaller.Upgrade(kubectlClient, tillerOptions)

		log.StopWait()

		if err != nil {
			return err
		}
	}

	tillerWaitingTime := 2 * 60 * time.Second
	tillerCheckInterval := 5 * time.Second

	log.StartWait("Waiting for Tiller server to start")

	for tillerWaitingTime > 0 {
		tillerDeployment, _ := kubectlClient.ExtensionsV1beta1().Deployments(privateConfig.Cluster.TillerNamespace).Get(tillerDeploymentName, metav1.GetOptions{})

		if tillerDeployment.Status.ReadyReplicas == tillerDeployment.Status.Replicas {
			break
		}

		time.Sleep(tillerCheckInterval)
		tillerWaitingTime = tillerWaitingTime - tillerCheckInterval
	}

	log.StopWait()
	log.Done("Tiller server started")

	return nil
}

// DeleteTiller clears the tiller server, the service account and role binding
func DeleteTiller(kubectlClient *kubernetes.Clientset, privateConfig *v1.PrivateConfig) error {
	errs := make([]error, 0, 1)

	err := kubectlClient.ExtensionsV1beta1().Deployments(privateConfig.Cluster.TillerNamespace).Delete(tillerDeploymentName, &metav1.DeleteOptions{})

	if err != nil {
		errs = append(errs, err)
	}

	err = kubectlClient.CoreV1().ServiceAccounts(privateConfig.Cluster.TillerNamespace).Delete(tillerServiceAccountName, &metav1.DeleteOptions{})

	if err != nil {
		errs = append(errs, err)
	}

	deleteRoleNames := []string{
		"tiller-config-manager",
		tillerRoleName,
	}

	deleteRoleNamespaces := []string{
		privateConfig.Cluster.TillerNamespace,
		privateConfig.Release.Namespace,
	}

	for key, value := range deleteRoleNames {
		err = kubectlClient.RbacV1beta1().Roles(deleteRoleNamespaces[key]).Delete(value, &metav1.DeleteOptions{})

		if err != nil {
			errs = append(errs, err)
		}

		err = kubectlClient.RbacV1beta1().RoleBindings(deleteRoleNamespaces[key]).Delete(value+"-binding", &metav1.DeleteOptions{})

		if err != nil {
			errs = append(errs, err)
		}
	}

	// Merge errors
	errorText := ""

	for _, value := range errs {
		errorText += value.Error() + "\n"
	}

	if errorText == "" {
		return nil
	} else {
		return errors.New(errorText)
	}
}

// func (helmClientWrapper *HelmClientWrapper) ensureAuth(namespace string) error {
//	 return ensureRoleBinding(helmClientWrapper.kubectl, tillerRoleName, namespace, helmClientWrapper.Settings.TillerNamespace, defaultPolicyRules)
// }

func ensureRoleBinding(kubectlClient *kubernetes.Clientset, name, namespace string, tillerNamespace string, rules []k8sv1beta1.PolicyRule) error {
	role := &k8sv1beta1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Rules: rules,
	}
	rolebinding := &k8sv1beta1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-binding",
			Namespace: namespace,
		},
		Subjects: []k8sv1beta1.Subject{
			{
				Kind:      k8sv1beta1.ServiceAccountKind,
				Name:      tillerServiceAccountName,
				Namespace: tillerNamespace,
			},
		},
		RoleRef: k8sv1beta1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     role.Name,
		},
	}
	kubectlClient.RbacV1beta1().Roles(namespace).Create(role)
	kubectlClient.RbacV1beta1().RoleBindings(namespace).Create(rolebinding)

	return nil
}

func (helmClientWrapper *HelmClientWrapper) updateRepos() error {
	allRepos, err := repo.LoadRepositoriesFile(helmClientWrapper.Settings.Home.RepositoryFile())

	if err != nil {
		return err
	}
	repos := []*repo.ChartRepository{}

	for _, repoData := range allRepos.Repositories {
		repo, err := repo.NewChartRepository(repoData, getter.All(*helmClientWrapper.Settings))

		if err != nil {
			return err
		}
		repos = append(repos, repo)
	}
	wg := sync.WaitGroup{}

	for _, re := range repos {
		wg.Add(1)

		go func(re *repo.ChartRepository) {
			defer wg.Done()

			err := re.DownloadIndexFile(helmClientWrapper.Settings.Home.String())

			if err != nil {
				log.With(err).Error("Unable to download repo index")

				//TODO
			}
		}(re)
	}

	wg.Wait()

	return nil
}

// ReleaseExists checks if the given release name exists
func (helmClientWrapper *HelmClientWrapper) ReleaseExists(releaseName string) (bool, error) {
	_, releaseHistoryErr := helmClientWrapper.Client.ReleaseHistory(releaseName, k8shelm.WithMaxHistory(1))

	if releaseHistoryErr != nil {
		if strings.Contains(releaseHistoryErr.Error(), helmstoragedriver.ErrReleaseNotFound(releaseName).Error()) {
			return false, nil
		}
		return false, releaseHistoryErr
	}
	return true, nil
}

// InstallChartByPath installs the given chartpath und the releasename in the releasenamespace
func (helmClientWrapper *HelmClientWrapper) InstallChartByPath(releaseName string, releaseNamespace string, chartPath string, values *map[interface{}]interface{}) (*hapi_release5.Release, error) {
	chart, chartLoadingErr := helmchartutil.Load(chartPath)

	if chartLoadingErr != nil {
		return nil, chartLoadingErr
	}

	chartDependencies := chart.GetDependencies()

	if len(chartDependencies) > 0 {
		_, chartReqError := helmchartutil.LoadRequirements(chart)

		if chartReqError != nil {
			return nil, chartReqError
		}
		chartDownloader := &helmdownloader.Manager{
		/*		Out:        i.out,
				ChartPath:  i.chartPath,
				HelmHome:   settings.Home,
				Keyring:    defaultKeyring(),
				SkipUpdate: false,
				Getters:    getter.All(settings),
		*/
		}
		chartDownloadErr := chartDownloader.Update()

		if chartDownloadErr != nil {
			return nil, chartDownloadErr
		}
		chart, chartLoadingErr = helmchartutil.Load(chartPath)

		if chartLoadingErr != nil {
			return nil, chartLoadingErr
		}
	}
	releaseExists, releaseExistsErr := helmClientWrapper.ReleaseExists(releaseName)

	if releaseExistsErr != nil {
		return nil, releaseExistsErr
	}
	deploymentTimeout := int64(10 * 60)
	overwriteValues := []byte("")

	if values != nil {
		unmarshalledValues, yamlErr := yaml.Marshal(*values)

		if yamlErr != nil {
			return nil, yamlErr
		}
		overwriteValues = unmarshalledValues
	}
	var release *hapi_release5.Release

	if releaseExists {
		upgradeResponse, releaseUpgradeErr := helmClientWrapper.Client.UpdateRelease(
			releaseName,
			chartPath,
			k8shelm.UpgradeTimeout(deploymentTimeout),
			k8shelm.UpdateValueOverrides(overwriteValues),
			k8shelm.ReuseValues(false),
			k8shelm.UpgradeWait(true),
		)

		if releaseUpgradeErr != nil {
			return nil, releaseUpgradeErr
		}
		release = upgradeResponse.GetRelease()
	} else {
		installResponse, releaseInstallErr := helmClientWrapper.Client.InstallReleaseFromChart(
			chart,
			releaseNamespace,
			k8shelm.InstallTimeout(deploymentTimeout),
			k8shelm.ValueOverrides(overwriteValues),
			k8shelm.ReleaseName(releaseName),
			k8shelm.InstallReuseName(false),
			k8shelm.InstallWait(true),
		)

		if releaseInstallErr != nil {
			return nil, releaseInstallErr
		}
		release = installResponse.GetRelease()
	}
	return release, nil
}

// InstallChartByName installs the given chart by name under the releasename in the releasenamespace
func (helmClientWrapper *HelmClientWrapper) InstallChartByName(releaseName string, releaseNamespace string, chartName string, chartVersion string, values *map[interface{}]interface{}) (*hapi_release5.Release, error) {
	if len(chartVersion) == 0 {
		chartVersion = ">0.0.0-0"
	}
	getter := getter.All(*helmClientWrapper.Settings)
	chartDownloader := downloader.ChartDownloader{
		HelmHome: helmClientWrapper.Settings.Home,
		Out:      os.Stdout,
		Getters:  getter,
		Verify:   downloader.VerifyNever,
	}
	os.MkdirAll(helmClientWrapper.Settings.Home.Archive(), os.ModePerm)

	chartPath, _, chartDownloadErr := chartDownloader.DownloadTo(chartName, chartVersion, helmClientWrapper.Settings.Home.Archive())

	if chartDownloadErr != nil {
		return nil, chartDownloadErr
	}
	return helmClientWrapper.InstallChartByPath(releaseName, releaseNamespace, chartPath, values)
}

// DeleteRelease deletes a helm release and optionally purges it
func (helmClientWrapper *HelmClientWrapper) DeleteRelease(releaseName string, purge bool) (*rls.UninstallReleaseResponse, error) {
	return helmClientWrapper.Client.DeleteRelease(releaseName, k8shelm.DeletePurge(purge))
}
