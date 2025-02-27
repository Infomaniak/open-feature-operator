package flagsourceconfiguration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/open-feature/open-feature-operator/apis/core/v1alpha1"
	"github.com/open-feature/open-feature-operator/controllers/common"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFlagSourceConfigurationReconciler_Reconcile(t *testing.T) {
	const (
		testNamespace  = "test-namespace"
		fsConfigName   = "test-config"
		deploymentName = "test-deploy"
	)

	tests := []struct {
		name                            string
		fsConfig                        *v1alpha1.FlagSourceConfiguration
		deployment                      *appsv1.Deployment
		restartedAtValueBeforeReconcile string
		restartedAtValueAfterReconcile  string
		flagdProxyDeployment            bool
	}{
		{
			name:                            "deployment gets restarted with rollout",
			fsConfig:                        createTestFSConfig(fsConfigName, testNamespace, deploymentName, true, v1alpha1.SyncProviderHttp),
			deployment:                      createTestDeployment(fsConfigName, testNamespace, deploymentName),
			restartedAtValueBeforeReconcile: "",
			restartedAtValueAfterReconcile:  time.Now().Format(time.RFC3339),
		},
		{
			name:                            "deployment without rollout",
			fsConfig:                        createTestFSConfig(fsConfigName, testNamespace, deploymentName, false, v1alpha1.SyncProviderHttp),
			deployment:                      createTestDeployment(fsConfigName, testNamespace, deploymentName),
			restartedAtValueBeforeReconcile: "",
			restartedAtValueAfterReconcile:  "",
		},
		{
			name:                            "no deployment",
			fsConfig:                        createTestFSConfig(fsConfigName, testNamespace, deploymentName, true, v1alpha1.SyncProviderHttp),
			deployment:                      nil,
			restartedAtValueBeforeReconcile: "",
			restartedAtValueAfterReconcile:  "",
		},
		{
			name:                            "no deployment, kube proxy deployment",
			fsConfig:                        createTestFSConfig(fsConfigName, testNamespace, deploymentName, true, v1alpha1.SyncProviderFlagdProxy),
			deployment:                      nil,
			restartedAtValueBeforeReconcile: "",
			restartedAtValueAfterReconcile:  "",
			flagdProxyDeployment:            true,
		},
	}

	err := v1alpha1.AddToScheme(scheme.Scheme)
	require.Nil(t, err)

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: testNamespace,
			Name:      fsConfigName,
		},
	}

	ctx := context.TODO()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// setting up fake k8s client
			var fakeClient client.Client
			if tt.deployment != nil {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tt.fsConfig, tt.deployment).WithIndex(&appsv1.Deployment{}, fmt.Sprintf("%s/%s", common.OpenFeatureAnnotationPath, common.FlagSourceConfigurationAnnotation), common.FlagSourceConfigurationIndex).Build()
			} else {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tt.fsConfig).WithIndex(&appsv1.Deployment{}, fmt.Sprintf("%s/%s", common.OpenFeatureAnnotationPath, common.FlagSourceConfigurationAnnotation), common.FlagSourceConfigurationIndex).Build()
			}
			kpConfig, err := common.NewFlagdProxyConfiguration()
			require.Nil(t, err)

			kpConfig.Namespace = testNamespace
			kph := common.NewFlagdProxyHandler(
				kpConfig,
				fakeClient,
				ctrl.Log.WithName("flagsourceconfiguration-FlagdProxyhandler"),
			)

			r := &FlagSourceConfigurationReconciler{
				Client:     fakeClient,
				Log:        ctrl.Log.WithName("flagsourceconfiguration-controller"),
				Scheme:     fakeClient.Scheme(),
				FlagdProxy: kph,
			}

			if tt.deployment != nil {
				// checking that the deployment does have 'restartedAt' set to the expected value before reconciliation
				deployment := &appsv1.Deployment{}
				err = fakeClient.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: testNamespace}, deployment)
				require.Nil(t, err)
				restartAt := deployment.Spec.Template.ObjectMeta.Annotations["kubectl.kubernetes.io/restartedAt"]
				require.Equal(t, tt.restartedAtValueBeforeReconcile, restartAt)
			}

			// running reconcile function
			_, err = r.Reconcile(ctx, req)
			require.Nil(t, err)

			if tt.deployment != nil {
				// checking that the deployment does have 'restartedAt' set to the expected value after reconciliation
				deployment := &appsv1.Deployment{}
				err = fakeClient.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: testNamespace}, deployment)
				require.Nil(t, err)

				require.Equal(t, tt.restartedAtValueAfterReconcile, deployment.Spec.Template.ObjectMeta.Annotations["kubectl.kubernetes.io/restartedAt"])
			}

			if tt.flagdProxyDeployment {
				// check that a deployment exists in the default namespace with the correct image and tag
				// ensure that the associated service has also been deployed
				deployment := &appsv1.Deployment{}
				err = fakeClient.Get(ctx, types.NamespacedName{Name: common.FlagdProxyDeploymentName, Namespace: testNamespace}, deployment)
				require.Nil(t, err)
				require.Equal(t, len(deployment.Spec.Template.Spec.Containers), 1)
				require.Equal(t, len(deployment.Spec.Template.Spec.Containers[0].Ports), 2)
				require.Equal(t, deployment.Spec.Template.Spec.Containers[0].Image, fmt.Sprintf("%s:%s", common.DefaultFlagdProxyImage, common.DefaultFlagdProxyTag))

				service := &corev1.Service{}
				err = fakeClient.Get(ctx, types.NamespacedName{Name: common.FlagdProxyServiceName, Namespace: testNamespace}, service)
				require.Nil(t, err)
				require.Equal(t, len(service.Spec.Ports), 1)
				require.Equal(t, service.Spec.Ports[0].TargetPort.IntVal, deployment.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
			}
		})
	}
}

func createTestDeployment(fsConfigName string, testNamespace string, deploymentName string) *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						fmt.Sprintf("%s/%s", common.OpenFeatureAnnotationPath, common.FlagSourceConfigurationAnnotation): "true",
						fmt.Sprintf("%s/%s", common.OpenFeatureAnnotationRoot, common.FlagSourceConfigurationAnnotation): fmt.Sprintf("%s/%s", testNamespace, fsConfigName),
					},
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "busybox",
							Args: []string{
								"sleep",
								"1000",
							},
						},
					},
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
		},
	}

	return deployment
}

func createTestFSConfig(fsConfigName string, testNamespace string, deploymentName string, rollout bool, provider v1alpha1.SyncProviderType) *v1alpha1.FlagSourceConfiguration {
	fsConfig := &v1alpha1.FlagSourceConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fsConfigName,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.FlagSourceConfigurationSpec{
			Image: deploymentName,
			Sources: []v1alpha1.Source{
				{
					Source:   "my-source",
					Provider: provider,
				},
			},
			RolloutOnChange: &rollout,
		},
	}

	return fsConfig
}
