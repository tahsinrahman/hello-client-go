package main

import (
	"fmt"
	"log"
	"os/signal"
	"path/filepath"

	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"

	corev1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kutilAppsV1 "github.com/appscode/kutil/apps/v1"
	kutilCoreV1 "github.com/appscode/kutil/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		log.Fatal(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}

	apiAppsV1 := clientset.AppsV1()
	apiCoreV1 := clientset.CoreV1()
	apiExtensionsV1beta1 := clientset.ExtensionsV1beta1()

	configmap, err := apiCoreV1.ConfigMaps("default").Create(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-configs",
		},
		Data: map[string]string{
			"port": "1234",
		},
	})
	if err != nil {
		log.Println(err)
	}

	replicas := int32(1)
	securityContext := true

	deployment, err := apiAppsV1.Deployments("default").Create(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "docker-deployment-name",
			Labels: map[string]string{
				"name": "docker-deployment-label",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "docker-pod-label",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "docker-pod-name",
					Labels: map[string]string{
						"name": "docker-pod-label",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						corev1.Volume{
							Name: "dind-storage",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					Containers: []corev1.Container{
						corev1.Container{
							Name:  "docker-dind",
							Image: "docker:18.09-dind",
							VolumeMounts: []corev1.VolumeMount{
								corev1.VolumeMount{
									Name:      "dind-storage",
									MountPath: "/var/lib/docker",
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &securityContext,
							},
						},
						corev1.Container{
							Name:    "docker-container",
							Image:   "docker:18.09",
							Command: []string{"/bin/sh", "-c"},
							Args:    []string{"docker run -p 4000:$PORT tahsin/booklist-api:0.0.1 --port=$PORT"},
							Ports: []corev1.ContainerPort{
								corev1.ContainerPort{
									Name:          "booklist-port",
									ContainerPort: 4000,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								corev1.EnvVar{
									Name:  "DOCKER_HOST",
									Value: "tcp://localhost:2375",
								},
								corev1.EnvVar{
									Name: "PORT",
									ValueFrom: &corev1.EnvVarSource{
										ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "api-configs",
											},
											Key: "port",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		log.Println(err)
	}

	service, err := apiCoreV1.Services("default").Create(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "docker-service",
			Labels: map[string]string{
				"name": "docker-service-label",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"name": "docker-pod-label",
			},
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				corev1.ServicePort{
					Name:       "service-port",
					Protocol:   corev1.ProtocolTCP,
					Port:       8888,
					TargetPort: intstr.FromString("booklist-port"),
				},
			},
		},
	})
	if err != nil {
		log.Println(err)
	}

	ingress, err := apiExtensionsV1beta1.Ingresses("default").Create(&extv1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "booklist-ingress",
			Labels: map[string]string{
				"name": "booklist-ingress-label",
			},
		},
		Spec: extv1beta1.IngressSpec{
			Rules: []extv1beta1.IngressRule{
				extv1beta1.IngressRule{
					Host: "mybooklist.com",
					IngressRuleValue: extv1beta1.IngressRuleValue{
						HTTP: &extv1beta1.HTTPIngressRuleValue{
							Paths: []extv1beta1.HTTPIngressPath{
								extv1beta1.HTTPIngressPath{
									Path: "/",
									Backend: extv1beta1.IngressBackend{
										ServiceName: "doker-service",
										ServicePort: intstr.FromString("service-port"),
									},
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		log.Println(err)
	}

	fmt.Println("waiting for deployment to be created")
	if err = wait.PollInfinite(time.Second*5,
		func() (done bool, err error) {
			done = false
			deployment, err = apiAppsV1.Deployments("default").Get(deployment.Name, metav1.GetOptions{})
			if err != nil {
				return done, err
			}
			done = *deployment.Spec.Replicas == replicas
			return done, err
		},
	); err != nil {
		log.Println("error while creating deployment, ", err)
	}
	fmt.Println("deployment created")

	replicas = int32(2)

	deployment, _, err = kutilAppsV1.CreateOrPatchDeployment(
		clientset,
		deployment.ObjectMeta,
		func(deployment *appsv1.Deployment) *appsv1.Deployment {
			deployment.Spec.Replicas = &replicas
			return deployment
		},
	)
	if err != nil {
		log.Println(err)
	}

	fmt.Println("waiting for deployment to be scaled")
	if err = wait.PollInfinite(time.Second*5,
		func() (done bool, err error) {
			done = false
			deployment, err = apiAppsV1.Deployments("default").Get(deployment.Name, metav1.GetOptions{})
			if err != nil {
				return done, err
			}
			done = *deployment.Spec.Replicas == replicas
			return done, err
		},
	); err != nil {
		log.Println("error while scaling deployment, ", err)
	}
	fmt.Println("deployment scaled")

	service, _, err = kutilCoreV1.PatchService(
		clientset,
		service,
		func(service *corev1.Service) *corev1.Service {
			service.Spec.Type = corev1.ServiceTypeNodePort
			return service
		},
	)

	fmt.Println("waiting for service to be updated")
	if err = wait.PollInfinite(time.Second*5,
		func() (done bool, err error) {
			done = false
			service, err = apiCoreV1.Services("default").Get(service.Name, metav1.GetOptions{})
			if err != nil {
				return done, err
			}
			done = service.Spec.Type == corev1.ServiceTypeNodePort
			return done, err
		},
	); err != nil {
		log.Println("error while updating service, ", err)
	}
	fmt.Println("service updated")

	nodePort := service.Spec.Ports[0].NodePort
	fmt.Printf("NodePort = %v\n", nodePort)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)

	<-ch

	log.Println("Shutting Down")

	apiCoreV1.ConfigMaps("default").Delete(configmap.Name, &metav1.DeleteOptions{})
	apiAppsV1.Deployments("default").Delete(deployment.Name, &metav1.DeleteOptions{})
	apiCoreV1.Services("default").Delete(service.Name, &metav1.DeleteOptions{})
	apiExtensionsV1beta1.Ingresses("default").Delete(ingress.Name, &metav1.DeleteOptions{})
}
