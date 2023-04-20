package wasm

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	apiv1 "k8s.io/api/core/v1"
	k8s_err "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	fv1 "github.com/fission/fission/pkg/apis/core/v1"
	otelUtils "github.com/fission/fission/pkg/utils/otel"
)

func (wasm *Wasm) getSvPort(fn *fv1.Function) (port int32, err error) {
	if fn.Spec.PodSpec == nil {
		return port, fmt.Errorf("podspec is empty for function %s", fn.ObjectMeta.Name)
	}
	if len(fn.Spec.PodSpec.Containers) != 1 {
		return port, fmt.Errorf("podspec should have exactly one container %s", fn.ObjectMeta.Name)
	}
	if len(fn.Spec.PodSpec.Containers[0].Ports) != 1 {
		return port, fmt.Errorf("container should have exactly one port %s", fn.ObjectMeta.Name)
	}
	return fn.Spec.PodSpec.Containers[0].Ports[0].ContainerPort, nil
}

func (wasm *Wasm) createOrGetSvc(ctx context.Context, fn *fv1.Function, deployLabels map[string]string, deployAnnotations map[string]string, svcName string, svcNamespace string) (*apiv1.Service, error) {
	targetPort, err := wasm.getSvPort(fn)
	if err != nil {
		return nil, err
	}
	logger := otelUtils.LoggerWithTraceID(ctx, wasm.logger)
	service := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        svcName,
			Labels:      deployLabels,
			Annotations: deployAnnotations,
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{
				{
					Name:       "http-env",
					Port:       int32(80),
					TargetPort: intstr.FromInt(int(targetPort)),
				},
			},
			Selector: deployLabels,
			Type:     apiv1.ServiceTypeClusterIP,
		},
	}

	existingSvc, err := wasm.kubernetesClient.CoreV1().Services(svcNamespace).Get(ctx, svcName, metav1.GetOptions{})
	if err == nil {
		// to adopt orphan service
		if existingSvc.Annotations[fv1.EXECUTOR_INSTANCEID_LABEL] != wasm.instanceID {
			existingSvc.Annotations = service.Annotations
			existingSvc.Labels = service.Labels
			existingSvc.Spec.Ports = service.Spec.Ports
			existingSvc.Spec.Selector = service.Spec.Selector
			existingSvc.Spec.Type = service.Spec.Type
			existingSvc, err = wasm.kubernetesClient.CoreV1().Services(svcNamespace).Update(ctx, existingSvc, metav1.UpdateOptions{})
			if err != nil {
				logger.Warn("error adopting service", zap.Error(err),
					zap.String("service", svcName), zap.String("ns", svcNamespace))
				return nil, err
			}
		}
		return existingSvc, err
	} else if k8s_err.IsNotFound(err) {
		svc, err := wasm.kubernetesClient.CoreV1().Services(svcNamespace).Create(ctx, service, metav1.CreateOptions{})
		if err != nil {
			if k8s_err.IsAlreadyExists(err) {
				svc, err = wasm.kubernetesClient.CoreV1().Services(svcNamespace).Get(ctx, svcName, metav1.GetOptions{})
			}
			if err != nil {
				return nil, err
			}
		}
		otelUtils.SpanTrackEvent(ctx, "svcCreated", otelUtils.GetAttributesForSvc(svc)...)
		return svc, nil
	}
	return nil, err
}

func (wasm *Wasm) deleteSvc(ctx context.Context, ns string, name string) error {
	return wasm.kubernetesClient.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})
}
