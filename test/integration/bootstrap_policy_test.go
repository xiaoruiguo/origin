package integration

import (
	"io/ioutil"
	"testing"
	"time"

	kapierror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	authorizationapi "github.com/openshift/origin/pkg/authorization/api"
	"github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/server/admin"
	originrest "github.com/openshift/origin/pkg/cmd/server/origin/rest"
	"github.com/openshift/origin/pkg/cmd/util/tokencmd"
	testutil "github.com/openshift/origin/test/util"
	testserver "github.com/openshift/origin/test/util/server"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBootstrapPolicyAuthenticatedUsersAgainstOpenshiftNamespace(t *testing.T) {
	testutil.RequireEtcd(t)
	defer testutil.DumpEtcdOnFailure(t)

	_, clusterAdminKubeConfig, err := testserver.StartTestMasterAPI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clusterAdminClientConfig, err := testutil.GetClusterAdminClientConfig(clusterAdminKubeConfig)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	valerieClientConfig := *clusterAdminClientConfig
	valerieClientConfig.Username = ""
	valerieClientConfig.Password = ""
	valerieClientConfig.BearerToken = ""
	valerieClientConfig.CertFile = ""
	valerieClientConfig.KeyFile = ""
	valerieClientConfig.CertData = nil
	valerieClientConfig.KeyData = nil

	accessToken, err := tokencmd.RequestToken(&valerieClientConfig, nil, "valerie", "security!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	valerieClientConfig.BearerToken = accessToken
	valerieOpenshiftClient, err := client.New(&valerieClientConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	openshiftSharedResourcesNamespace := "openshift"

	if _, err := valerieOpenshiftClient.Templates(openshiftSharedResourcesNamespace).List(metav1.ListOptions{}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := valerieOpenshiftClient.Templates(metav1.NamespaceDefault).List(metav1.ListOptions{}); err == nil || !kapierror.IsForbidden(err) {
		t.Errorf("unexpected error: %v", err)
	}

	if _, err := valerieOpenshiftClient.ImageStreams(openshiftSharedResourcesNamespace).List(metav1.ListOptions{}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := valerieOpenshiftClient.ImageStreams(metav1.NamespaceDefault).List(metav1.ListOptions{}); err == nil || !kapierror.IsForbidden(err) {
		t.Errorf("unexpected error: %v", err)
	}

	if _, err := valerieOpenshiftClient.ImageStreamTags(openshiftSharedResourcesNamespace).Get("name", "tag"); !kapierror.IsNotFound(err) {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := valerieOpenshiftClient.ImageStreamTags(metav1.NamespaceDefault).Get("name", "tag"); err == nil || !kapierror.IsForbidden(err) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBootstrapPolicyOverwritePolicyCommand(t *testing.T) {
	testutil.RequireEtcd(t)
	defer testutil.DumpEtcdOnFailure(t)

	masterConfig, clusterAdminKubeConfig, err := testserver.StartTestMasterAPI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	client, err := testutil.GetClusterAdminClient(clusterAdminKubeConfig)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if err := client.ClusterPolicies().Delete(authorizationapi.PolicyName); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// after the policy is deleted, we must wait for it to be cleared from the policy cache
	err = wait.Poll(10*time.Millisecond, 10*time.Second, func() (bool, error) {
		_, err := client.ClusterPolicies().List(metav1.ListOptions{})
		if err == nil {
			return false, nil
		}
		if !kapierror.IsForbidden(err) {
			t.Errorf("unexpected error: %v", err)
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("timeout: %v", err)
	}

	optsGetter, err := originrest.StorageOptions(*masterConfig)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if err := admin.OverwriteBootstrapPolicy(optsGetter, masterConfig.PolicyConfig.BootstrapPolicyFile, admin.CreateBootstrapPolicyFileFullCommand, true, ioutil.Discard); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if _, err := client.ClusterPolicies().List(metav1.ListOptions{}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBootstrapPolicySelfSubjectAccessReviews(t *testing.T) {
	testutil.RequireEtcd(t)
	defer testutil.DumpEtcdOnFailure(t)

	_, clusterAdminKubeConfig, err := testserver.StartTestMasterAPI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clusterAdminClientConfig, err := testutil.GetClusterAdminClientConfig(clusterAdminKubeConfig)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	valerieOpenshiftClient, valerieKubeClient, _, err := testutil.GetClientForUser(*clusterAdminClientConfig, "valerie")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	askCanICreatePolicyBindings := &authorizationapi.LocalSubjectAccessReview{
		Action: authorizationapi.Action{Verb: "create", Resource: "policybindings"},
	}
	subjectAccessReviewTest{
		description:       "can I get a subjectaccessreview on myself even if I have no rights to do it generally",
		localInterface:    valerieOpenshiftClient.LocalSubjectAccessReviews("openshift"),
		localReview:       askCanICreatePolicyBindings,
		kubeAuthInterface: valerieKubeClient.Authorization(),
		response: authorizationapi.SubjectAccessReviewResponse{
			Allowed:   false,
			Reason:    `User "valerie" cannot create policybindings in project "openshift"`,
			Namespace: "openshift",
		},
	}.run(t)

	askCanClusterAdminsCreateProject := &authorizationapi.LocalSubjectAccessReview{
		Groups: sets.NewString("system:cluster-admins"),
		Action: authorizationapi.Action{Verb: "create", Resource: "projects"},
	}
	subjectAccessReviewTest{
		description:       "I shouldn't be allowed to ask whether someone else can perform an action",
		localInterface:    valerieOpenshiftClient.LocalSubjectAccessReviews("openshift"),
		localReview:       askCanClusterAdminsCreateProject,
		kubeAuthInterface: valerieKubeClient.Authorization(),
		kubeNamespace:     "openshift",
		err:               `User "valerie" cannot create localsubjectaccessreviews in project "openshift"`,
		kubeErr:           `User "valerie" cannot create localsubjectaccessreviews.authorization.k8s.io in project "openshift"`,
	}.run(t)

}

func TestSelfSubjectAccessReviewsNonExistingNamespace(t *testing.T) {
	testutil.RequireEtcd(t)
	defer testutil.DumpEtcdOnFailure(t)

	_, clusterAdminKubeConfig, err := testserver.StartTestMasterAPI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clusterAdminClientConfig, err := testutil.GetClusterAdminClientConfig(clusterAdminKubeConfig)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	valerieOpenshiftClient, valerieKubeClient, _, err := testutil.GetClientForUser(*clusterAdminClientConfig, "valerie")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ensure that a SAR for a non-exisitng namespace gives a SAR response and not a
	// namespace doesn't exist response from admisison.
	askCanICreatePodsInNonExistingNamespace := &authorizationapi.LocalSubjectAccessReview{
		Action: authorizationapi.Action{Namespace: "foo", Verb: "create", Resource: "pods"},
	}
	subjectAccessReviewTest{
		description:       "ensure SAR for non-existing namespace does not leak namespace info",
		localInterface:    valerieOpenshiftClient.LocalSubjectAccessReviews("foo"),
		localReview:       askCanICreatePodsInNonExistingNamespace,
		kubeAuthInterface: valerieKubeClient.Authorization(),
		response: authorizationapi.SubjectAccessReviewResponse{
			Allowed:   false,
			Reason:    `User "valerie" cannot create pods in project "foo"`,
			Namespace: "foo",
		},
	}.run(t)
}
