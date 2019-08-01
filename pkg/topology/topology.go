//Package topology Defines the Reconciliation logic and required setup for topology collector CR.
// IBM Confidential
// OCO Source Materials
// 5737-E67
// (C) Copyright IBM Corporation 2019 All Rights Reserved
// The source code for this program is not published or otherwise divested of its trade secrets, irrespective of what has been deposited with the U.S. Copyright Office.
package topology

import (
	"context"
	"strings"

	openshiftsecurityv1 "github.com/openshift/api/security/v1"
	klusterletv1alpha1 "github.ibm.com/IBMPrivateCloud/ibm-klusterlet-operator/pkg/apis/klusterlet/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("topology")

// Reconcile Resolves differences in the running state of the connection manager services and CRDs.
func Reconcile(instance *klusterletv1alpha1.KlusterletService, client client.Client, scheme *runtime.Scheme) error {
	reqLogger := log.WithValues("KlusterletService.Namespace", instance.Namespace, "KlusterletService.Name", instance.Name)
	reqLogger.Info("Reconciling TopologyCollector")

	topologyCollectorCR, err := newTopologyCollectorCR(instance, client)
	if err != nil {
		log.Error(err, "Fail to generate desired TopologyCollector CR")
		return err
	}

	err = controllerutil.SetControllerReference(instance, topologyCollectorCR, scheme)
	if err != nil {
		log.Error(err, "Error setting controller reference")
		return err
	}
	// TODO(tonytran): split up weavescope and topology collector
	foundTopologyCollector := &klusterletv1alpha1.TopologyCollector{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: topologyCollectorCR.Name, Namespace: topologyCollectorCR.Namespace}, foundTopologyCollector)
	if err != nil {
		if errors.IsNotFound(err) {
			// TopologyCollector CR does NOT exist
			if instance.GetDeletionTimestamp() != nil {
				//CR existed, so now clean up other resources
				for i, finalizer := range instance.Finalizers {
					if finalizer == topologyCollectorCR.Name {
						log.Info("Cleaning up old resources")
						secretsToDeletes := []string{
							topologyCollectorCR.Name + "-ca-cert",
							topologyCollectorCR.Name + "-server-secret",
							topologyCollectorCR.Name + "-client-secret",
						}
						for _, secretToDelete := range secretsToDeletes {
							foundSecretToDelete := &corev1.Secret{}
							err := client.Get(context.TODO(), types.NamespacedName{Name: secretToDelete, Namespace: topologyCollectorCR.Namespace}, foundSecretToDelete)
							if err == nil {
								err = client.Delete(context.TODO(), foundSecretToDelete)
								if err != nil {
									log.Error(err, "Fail to DELETE TopologyCollector Secret", "Secret.Name", secretToDelete)
									return err
								}
							}
						}
						instance.Finalizers = append(instance.Finalizers[0:i], instance.Finalizers[i+1:]...)
						break
					}
				}
				//whether we found the finalizer or not, and there was no error we should just pass through
				return nil
			}

			if instance.Spec.TopologyCollectorConfig.Enabled {
				err = createServiceAccount(client, scheme, instance, topologyCollectorCR)
				if err != nil {
					log.Error(err, "Fail to CREATE ServiceAccount for TopologyCollector", "TopologyCollector.Name", topologyCollectorCR.Name)
					return err
				}

				log.Info("Creating a new TopologyCollector", "TopologyCollector.Namespace", topologyCollectorCR.Namespace, "ConnectionManager.Name", topologyCollectorCR.Name)
				err = client.Create(context.TODO(), topologyCollectorCR)
				if err != nil {
					log.Error(err, "Fail to CREATE TopologyCollector CR")
					return err
				}

				instance.Finalizers = append(instance.Finalizers, topologyCollectorCR.Name)
				reqLogger.Info("Successfully Reconciled TopologyCollector")
				return nil
			}
			reqLogger.Info("Successfully Reconciled TopologyCollector")
			return nil
		}

		log.Error(err, "Unexpected ERROR")
		return err
	}

	if foundTopologyCollector.GetDeletionTimestamp() == nil {
		if instance.GetDeletionTimestamp() != nil || !instance.Spec.TopologyCollectorConfig.Enabled {
			err = client.Delete(context.TODO(), topologyCollectorCR)
			if err != nil {
				log.Error(err, "Fail to DELETE TopologyCollector CR")
				return err
			}

			reqLogger.Info("Successfully Reconciled TopologyCollector")
			return nil
		}

		// KlusterletService NOT in deletion state AND found, update
		foundTopologyCollector.Spec = topologyCollectorCR.Spec
		err = client.Update(context.TODO(), foundTopologyCollector)
		if err != nil {
			log.Error(err, "Fail to UPDATE TopologyCollector CR")
			return nil
		}
	}

	reqLogger.Info("Successfully Reconciled TopologyCollector")
	return nil
}

func newTopologyCollectorCR(cr *klusterletv1alpha1.KlusterletService, client client.Client) (*klusterletv1alpha1.TopologyCollector, error) {
	labels := map[string]string{
		"app": cr.Name,
	}

	weaveImage, err := cr.GetImage("weave")
	if err != nil {
		log.Error(err, "Fail to get Image", "Component.Name", "weave")
		return nil, err
	}

	collectorImage, err := cr.GetImage("collector")
	if err != nil {
		log.Error(err, "Fail to get Image", "Component.Name", "collector")
		return nil, err
	}

	routerImage, err := cr.GetImage("router")
	if err != nil {
		log.Error(err, "Fail to get Image", "Component.Name", "routers")
		return nil, err
	}

	return &klusterletv1alpha1.TopologyCollector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-topology",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: klusterletv1alpha1.TopologyCollectorSpec{
			FullNameOverride:  cr.Name + "-topology",
			ClusterName:       cr.Spec.ClusterName,
			ClusterNamespace:  cr.Spec.ClusterNamespace,
			ConnectionManager: cr.Name + "-connmgr",
			ContainerRuntime:  determineRuntime(client),
			Enabled:           true,
			UpdateInterval:    cr.Spec.TopologyCollectorConfig.CollectorUpdateInterval,
			CACertIssuer:      cr.Name + "-self-signed",
			ServiceAccount: klusterletv1alpha1.TopologyCollectorServiceAccount{
				Name: cr.Name + "-topology-collector",
			},
			WeaveImage:      weaveImage,
			CollectorImage:  collectorImage,
			RouterImage:     routerImage,
			ImagePullSecret: cr.Spec.ImagePullSecret,
		},
	}, nil
}

func determineRuntime(kubeclient client.Client) string {
	nodelist := &corev1.NodeList{}
	err := kubeclient.List(context.TODO(), &client.ListOptions{}, nodelist)
	if err != nil {
		log.Error(err, "Error listing nodes in cluster, assuming ContainerRuntime is docker")
		return "docker"
	}
	runtime := nodelist.Items[0].Status.NodeInfo.ContainerRuntimeVersion
	return strings.Split(runtime, ":")[0] //format of container runtime in node info is runtime://version
}

func createServiceAccount(client client.Client, scheme *runtime.Scheme, instance *klusterletv1alpha1.KlusterletService, topology *klusterletv1alpha1.TopologyCollector) error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      topology.Spec.ServiceAccount.Name,
			Namespace: topology.Namespace,
		},
	}
	err := controllerutil.SetControllerReference(instance, serviceAccount, scheme)
	if err != nil {
		return err
	}

	foundServiceAccount := &corev1.ServiceAccount{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: serviceAccount.Name, Namespace: serviceAccount.Namespace}, foundServiceAccount)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating ServiceAccount", "Name", serviceAccount.Name, "Namespace", serviceAccount.Namespace)
		err = client.Create(context.TODO(), serviceAccount)
		if err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}

	foundPrivilegedSCC := &openshiftsecurityv1.SecurityContextConstraints{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: "privileged", Namespace: ""}, foundPrivilegedSCC)
	// if client.Get return error that means no privileged SCC in that case skip adding user to scc and ignore error
	if err == nil {
		user := "system:serviceaccount:" + serviceAccount.Namespace + ":" + serviceAccount.Name
		log.Info("Adding User to SCC", "User", user, "SCC", foundPrivilegedSCC.Name)
		foundPrivilegedSCC.Users = append(foundPrivilegedSCC.Users, user)
		err = client.Update(context.TODO(), foundPrivilegedSCC)
		if err != nil {
			return err
		}
	}
	return nil
}