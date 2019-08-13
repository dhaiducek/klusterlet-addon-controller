// Package v1beta1 of appmgr provides a reconciler for the ApplicationManager
// IBM Confidential
// OCO Source Materials
// 5737-E67
// (C) Copyright IBM Corporation 2019 All Rights Reserved
// The source code for this program is not published or otherwise divested of its trade secrets, irrespective of what has been deposited with the U.S. Copyright Office.
package v1beta1

import (
	"context"

	multicloudv1beta1 "github.ibm.com/IBMPrivateCloud/ibm-klusterlet-operator/pkg/apis/multicloud/v1beta1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Reconcile Resolves differences in the running state of the cert-manager services and CRDs.
func Reconcile(instance *multicloudv1beta1.Endpoint, client client.Client, scheme *runtime.Scheme) (bool, error) {
	reqLogger := log.WithValues("Endpoint.Namespace", instance.Namespace, "Endpoint.Name", instance.Name)
	reqLogger.Info("Reconciling ApplicationManager")

	appMgrCR, err := newApplicationManagerCR(instance)
	if err != nil {
		log.Error(err, "Fail to generate desired ApplicationManager CR")
		return false, err
	}

	err = controllerutil.SetControllerReference(instance, appMgrCR, scheme)
	if err != nil {
		log.Error(err, "Unable to SetControllerReference")
		return false, err
	}

	foundAppMgrCR := &multicloudv1beta1.ApplicationManager{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: appMgrCR.Name, Namespace: appMgrCR.Namespace}, foundAppMgrCR)
	if err != nil {
		if errors.IsNotFound(err) {
			log.V(5).Info("ApplicationManager CR DOES NOT exist")
			if instance.GetDeletionTimestamp() == nil {
				log.V(5).Info("instance IS NOT in deletion state")
				if instance.Spec.ApplicationManagerConfig.Enabled {
					log.V(5).Info("ApplicationManager ENABLED")
					if err = create(instance, appMgrCR, client); err != nil {
						log.Error(err, "fail to CREATE ApplicationManager CR")
						return false, err
					}
				} else {
					log.V(5).Info("ApplicationManager DISABLED")
					if err = finalize(instance, appMgrCR, client); err != nil {
						log.Error(err, "fail to FINALIZE ApplicationManager CR")
						return false, err
					}
				}
			} else {
				log.V(5).Info("instance IS in deletion state")
				if err = finalize(instance, appMgrCR, client); err != nil {
					log.Error(err, "fail to FINALIZE ApplicationManager CR")
					return false, err
				}
			}
		} else {
			log.Error(err, "Unexpected ERROR")
			return false, err
		}
	} else {
		log.V(5).Info("ApplicationManager CR DOES exist")
		if foundAppMgrCR.GetDeletionTimestamp() == nil {
			log.V(5).Info("ApplicationManager CR IS NOT in deletion state")
			if instance.GetDeletionTimestamp() == nil && instance.Spec.ApplicationManagerConfig.Enabled {
				log.Info("instance IS NOT in deletion state and ApplicationManager ENABLED")
				err = update(instance, appMgrCR, foundAppMgrCR, client)
				if err != nil {
					log.Error(err, "fail to UPDATE ApplicationManager CR")
					return false, err
				}
			} else {
				log.V(5).Info("instance IS in deletion state or ApplicationManager DISABLED")
				if err = delete(foundAppMgrCR, client); err != nil {
					log.Error(err, "Fail to DELETE ApplicationManager CR")
					return false, err
				}
				reqLogger.Info("Requeueing Reconcile for ApplicationManager")
				return true, nil
			}
		} else {
			reqLogger.Info("Requeueing Reconcile for ApplicationManager")
			return true, nil
		}
	}

	reqLogger.Info("Successfully Reconciled ApplicationManager")
	return false, nil
}

func newApplicationManagerCR(instance *multicloudv1beta1.Endpoint) (*multicloudv1beta1.ApplicationManager, error) {
	labels := map[string]string{
		"app": instance.Name,
	}

	deployableImage, err := instance.GetImage("deployable")
	if err != nil {
		log.Error(err, "Fail to get Image", "Component.Name", "deployable")
		return nil, err
	}

	subscriptionImage, err := instance.GetImage("subscription")
	if err != nil {
		log.Error(err, "Fail to get Image", "Component.Name", "subscription")
		return nil, err
	}

	return &multicloudv1beta1.ApplicationManager{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-appmgr",
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: multicloudv1beta1.ApplicationManagerSpec{
			FullNameOverride:  instance.Name + "-appmgr",
			ConnectionManager: instance.Name + "-connmgr",
			ClusterName:       instance.Spec.ClusterName,
			ClusterNamespace:  instance.Spec.ClusterNamespace,
			DeployableSpec: multicloudv1beta1.ApplicationManagerDeployableSpec{
				Image: deployableImage,
			},
			SubscriptionSpec: multicloudv1beta1.ApplicationManagerSubscriptionSpec{
				Image: subscriptionImage,
			},
			ImagePullSecret: instance.Spec.ImagePullSecret,
		},
	}, nil
}