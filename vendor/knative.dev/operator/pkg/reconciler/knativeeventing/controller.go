/*
Copyright 2019 The Knative Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package knativeeventing

import (
	"context"
	"time"

	"github.com/go-logr/zapr"
	mfc "github.com/manifestival/client-go-client"
	mf "github.com/manifestival/manifestival"
	"go.uber.org/zap"
	apixclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/tools/cache"

	"knative.dev/operator/pkg/apis/operator/v1beta1"
	operatorclient "knative.dev/operator/pkg/client/injection/client"
	knativeEventinginformer "knative.dev/operator/pkg/client/injection/informers/operator/v1beta1/knativeeventing"
	knereconciler "knative.dev/operator/pkg/client/injection/reconciler/operator/v1beta1/knativeeventing"
	"knative.dev/operator/pkg/reconciler/common"
	kubeclient "knative.dev/pkg/client/injection/kube/client"
	deploymentinformer "knative.dev/pkg/client/injection/kube/informers/apps/v1/deployment"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/clients/dynamicclient"
	"knative.dev/pkg/logging"
	"k8s.io/apimachinery/pkg/util/wait"
)

// NewController initializes the controller and is called by the generated code
// Registers eventhandlers to enqueue events
func NewController(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
	return NewExtendedController(common.NoExtension)(ctx, cmw)
}

// NewExtendedController returns a controller extended to a specific platform
func NewExtendedController(generator common.ExtensionGenerator) injection.ControllerConstructor {
	return func(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
		knativeEventingInformer := knativeEventinginformer.Get(ctx)
		deploymentInformer := deploymentinformer.Get(ctx)
		kubeClient := kubeclient.Get(ctx)
		logger := logging.FromContext(ctx)

		mfclient, err := mfc.NewClient(injection.GetConfig(ctx))
		if err != nil {
			logger.Fatalw("Error creating client from injected config", zap.Error(err))
		}
		mflogger := zapr.NewLogger(logger.Named("manifestival").Desugar())
		manifest, _ := mf.ManifestFrom(mf.Slice{}, mf.UseClient(mfclient), mf.UseLogger(mflogger))

		c := &Reconciler{
			kubeClientSet:     kubeClient,
			operatorClientSet: operatorclient.Get(ctx),
			manifest:          manifest,
		}
		impl := knereconciler.NewImpl(ctx, c)
		c.extension = generator(ctx, impl)

		logger.Info("Setting up event handlers")

		knativeEventingInformer.Informer().AddEventHandler(controller.HandleAll(impl.Enqueue))

		deploymentInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
			FilterFunc: controller.FilterControllerGVK(v1beta1.SchemeGroupVersion.WithKind("KnativeEventing")),
			Handler:    controller.HandleAll(impl.EnqueueControllerOf),
		})

		go func(){
			err = wait.PollImmediate(3*time.Second, 5*time.Minute, func() (bool, error) {
				err = common.MigrateCustomResource(ctx, dynamicclient.Get(ctx), apixclient.NewForConfigOrDie(injection.GetConfig(ctx)))
				if err != nil {
					return false, nil
				}
				return true, nil
			})
			if err != nil {
				logger.Fatalw("Unable to migrate existing custom resources", zap.Error(err))
			}
		}()

		return impl
	}
}
