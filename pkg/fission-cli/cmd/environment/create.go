/*
Copyright 2019 The Fission Authors.

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

package environment

import (
	"context"
	"fmt"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sigs.k8s.io/yaml"
	"strings"
	"time"

	fv1 "github.com/fission/fission/pkg/apis/core/v1"
	"github.com/fission/fission/pkg/fission-cli/cliwrapper/cli"
	"github.com/fission/fission/pkg/fission-cli/cmd"
	"github.com/fission/fission/pkg/fission-cli/cmd/spec"
	"github.com/fission/fission/pkg/fission-cli/console"
	flagkey "github.com/fission/fission/pkg/fission-cli/flag/key"
	"github.com/fission/fission/pkg/fission-cli/util"
	"github.com/fission/fission/pkg/utils"
	corev1 "k8s.io/api/core/v1"
)

type CreateSubCommand struct {
	cmd.CommandActioner
	env *fv1.Environment
}

const HostPath = "/var/lib/kaniko/workplace"
const kanikoFilePath = "/var/lib/kaniko/files/pod.yaml"
const WasmRuntimeClass = "wasm"

func Create(input cli.Input) error {
	return (&CreateSubCommand{}).do(input)
}

func (opts *CreateSubCommand) do(input cli.Input) error {
	isWasmEnv := false
	err := opts.complete(input, &isWasmEnv)
	if err != nil {
		return err
	}
	return opts.run(input, isWasmEnv)
}

func getK8sClient() *kubernetes.Clientset {
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		log.Fatalf("failed to create kubeconfig: %v", err)
	}
	fmt.Println("Kubeconfig loaded successfully.")
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("failed to create Kubernetes client: %v", err)
	}
	fmt.Println("Kubernetes client created.")
	return clientset
}

func createWasmBuilderPod(clientset *kubernetes.Clientset, namespace, wasmBuilder, envImageName string) (string, error) {
	podName := "wasm-builder"
	// 定义 Pod
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "wasm-builder",
					Image: wasmBuilder,
					Env: []corev1.EnvVar{
						{
							Name:  "PROJECT_NAME",
							Value: envImageName,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "dockerfile-storage",
							MountPath: "/workspace",
						},
					},
					Command: []string{"/bin/bash", "-c", "--"},
					Args:    []string{"build_wasm.sh && exit 0 || exit 1"},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "dockerfile-storage",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "dockerfile-claim",
						},
					},
				},
			},
		},
	}

	yamlData, err := yaml.Marshal(pod)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Pod to YAML: %w", err)
	}

	fileName := "wasm-builder-pod.yaml"
	filePath := fmt.Sprintf("%s/%s", HostPath, fileName)
	err = os.WriteFile(filePath, yamlData, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write Pod YAML to file: %w", err)
	}

	cmd := exec.Command("kubectl", "apply", "-f", filePath)
	defer os.Remove(filePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to apply wasm-builder pod.yaml: %v, output: %s", err, string(output))
	}

	// 轮询检查 Pod 状态
	timeout := time.After(15 * time.Minute) // 设置超时时间
	for {
		select {
		case <-timeout:
			return "", fmt.Errorf("timeout waiting for Pod %s to complete", podName)
		default:
			// 获取 Pod 状态
			pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to get Pod status: %w", err)
			}

			// 检查 Pod 状态
			if pod.Status.Phase == corev1.PodSucceeded {
				fmt.Printf("Pod %s completed successfully and will be removed\n", podName)
				if err := clientset.CoreV1().Pods(namespace).Delete(context.TODO(), podName, metav1.DeleteOptions{}); err != nil {
					return "", fmt.Errorf("failed to delete Pod %s: %w", podName, err)
				}
				return podName, nil
			} else if pod.Status.Phase == corev1.PodFailed {
				return "", fmt.Errorf("Pod %s failed. Check logs using 'kubectl logs %s' and remove it with 'kubectl delete pod %s'", podName, podName, podName)
			}

			fmt.Printf("Waiting for Pod %s to complete...\n", podName)
			time.Sleep(1 * time.Second) // 每秒轮询一次
		}
	}

}

// complete creates a environment objects and populates it with default value and CLI inputs.
// 在 complete 中，检测 --image 内容，如果是 .wasm 结尾的文件，我们可以通过 kaniko 构建出镜像，使后面构建 env 时使用新的镜像本地地址。
// 如果不是，则默认使用镜像地址。
func (opts *CreateSubCommand) complete(input cli.Input, isWasmEnv *bool) error {
	envImageName := input.String(flagkey.EnvImage)
	envName := input.String(flagkey.EnvName)
	wasmBuilder := input.String(flagkey.EnvWasmBuilder)
	if wasmBuilder != "" {
		clientSet := getK8sClient()
		// 调用 createWasmBuilderPod 函数
		_, err := createWasmBuilderPod(clientSet, "fission", wasmBuilder, envImageName)
		if err != nil {
			log.Printf("Error occurred while creating Pod: %v", err)
			return err
		}
		fmt.Println("Pod wasm-builder created successfully")
		// 构建目标文件路径
		targetWasmFile := fmt.Sprintf("%s.wasm", envImageName)
		wasmFilePath := fmt.Sprintf(HostPath+"/%s.wasm", envImageName)
		// 检查目标文件是否存在
		if info, err := os.Stat(wasmFilePath); err == nil && !info.IsDir() {
			envImageName = targetWasmFile
			fmt.Printf("Compiled Wasm file exists: %s\n", wasmFilePath)
		} else {
			return fmt.Errorf("compiled Wasm file not found: %s", wasmFilePath)
		}
	}
	imageUrl := envImageName
	//如果以 .wasm 结尾，我们需要构建镜像并重新指定 imageUrl
	if checkWasmEnv(envName) {
		*isWasmEnv = true
	}
	if checkWasm(envImageName) {
		err := createDockerfile(envImageName)
		if err != nil {
			return fmt.Errorf("error building dockerfile: %w", err)
		}
		result, err := BuildImageWithKaniko(envName)
		if err != nil {
			return fmt.Errorf("error building image: %w", err)
		}
		imageUrl = result
		fmt.Println(result)
	}
	// 由于 flagkey 中的属性是常量，我们通过新建变量来指定 envImage
	env, err := createEnvironmentFromCmd(input, imageUrl, *isWasmEnv)
	if err != nil {
		return err
	}
	opts.env = env
	return nil
}

// 按照规则 在 hostpath 下构建 dockerfile
func createDockerfile(wasmFileName string) error {
	err := os.MkdirAll(HostPath, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create hostpath directory: %w", err)
	}
	dockerfilePath := filepath.Join(HostPath, "Dockerfile")
	content := fmt.Sprintf(`
FROM scratch
COPY %s /
ENTRYPOINT ["%s"]
`, wasmFileName, wasmFileName)

	file, err := os.Create(dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to create Dockerfile: %w", err)
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			fmt.Printf("Error closing file: %v\n", err)
		}
	}(file)

	_, err = file.WriteString(content)
	if err != nil {
		return fmt.Errorf("failed to write to Dockerfile: %w", err)
	}

	fmt.Println("Dockerfile created at:", dockerfilePath)
	return nil
}

// 使用 kaniko pod 将镜像打包
func BuildImageWithKaniko(envName string) (string, error) {
	cmd := exec.Command("kubectl", "apply", "-f", kanikoFilePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to apply pod.yaml: %v, output: %s", err, string(output))
	}
	fmt.Printf("Applied kaniko pod.yaml: %s\n", output)
	clientset := getK8sClient()
	podName := "kaniko"
	for {
		// Get Pod status
		pod, err := clientset.CoreV1().Pods("fission").Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get pod status: %v", err)
		}
		// Check if the Pod is completed
		if pod.Status.Phase == corev1.PodSucceeded {
			fmt.Printf("Pod %s completed successfully and will be removed immediately\n", podName)
			if err := clientset.CoreV1().Pods("fission").Delete(context.TODO(), podName, metav1.DeleteOptions{}); err != nil {
				return "", fmt.Errorf("failed to delete pod %s: %v", podName, err)
			}
			break
		} else if pod.Status.Phase == corev1.PodFailed {
			return "", fmt.Errorf("Pod %s failed, use [kubectl logs kaniko] to find problems. Please remove pod named [kaniko] by [kubectl delete pod kaniko]", podName)
		}
		fmt.Printf("Waiting for Pod %s to complete...\n", podName)
		time.Sleep(1 * time.Second) // Poll every 1 seconds
	}

	//此时如果成功打包，在 hostpath 下会有 image.tar 文件
	//需要使用 命令 ctr -a /run/containerd/containerd.sock -n k8s.io images import /hostpath/image.tar
	//然后使用命令 ctr -a /run/containerd/containerd.sock -n k8s.io images tag docker.io/library/image:latest k8s.io/xx-wasm:latest
	//xx-wasm 来自 函数的传参 envName
	//上面的步骤做完，镜像就导入到本地了，然后就需要清理资源：
	//pod 已经在上面的步骤保证删除了，然后把 hostpath 下的 Dockerfile 以及 image.tar 删除。
	imageTarPath := filepath.Join(HostPath, "image.tar")
	importCmd := exec.Command("ctr", "-a", "/run/containerd/containerd.sock", "-n", "k8s.io", "images", "import", imageTarPath)
	importOutput, err := importCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to import image.tar: %v, output: %s", err, string(importOutput))
	}
	fmt.Printf("Imported image from %s\n", imageTarPath)
	tagName := fmt.Sprintf("k8s.io/%s:latest", envName)
	tagCmd := exec.Command("ctr", "-a", "/run/containerd/containerd.sock", "-n", "k8s.io", "images", "tag", "docker.io/library/image:latest", tagName)
	tagOutput, err := tagCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to tag image as %s: %v, output: %s", tagName, err, string(tagOutput))
	}
	fmt.Printf("Tagged image as %s\n", tagName)
	cleanupCmd := exec.Command("rm", "-rf", HostPath+"/Dockerfile", HostPath+"/image.tar")
	if err := cleanupCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to clean up hostpath: %v", err)
	}
	fmt.Println("Cleaned up hostpath")
	return tagName, nil
}

// run write the resource to a spec file or create a fission CRD with remote fission server.
// It also prints warning/error if necessary.
func (opts *CreateSubCommand) run(input cli.Input, isWasmEnv bool) error {
	m := opts.env.ObjectMeta

	envList, err := opts.Client().V1().Environment().List(m.Namespace)
	if err != nil {
		return err
	} else if len(envList) > 0 {
		console.Verbose(2, "%d environment(s) are present in the %s namespace.  "+
			"These environments are not isolated from each other; use separate namespaces if you need isolation.",
			len(envList), m.Namespace)
	}

	// if we're writing a spec, don't call the API
	// save to spec file or display the spec to console
	if input.Bool(flagkey.SpecDry) {
		return spec.SpecDry(*opts.env)
	}

	if input.Bool(flagkey.SpecSave) {
		specFile := fmt.Sprintf("env-%v.yaml", m.Name)
		err = spec.SpecSave(*opts.env, specFile)
		if err != nil {
			return errors.Wrap(err, "error saving environment spec")
		}
		return nil
	}

	_, err = opts.Client().V1().Environment().Create(opts.env)
	if err != nil {
		return errors.Wrap(err, "error creating environment")
	}

	fmt.Printf("environment '%v' created\n", m.Name)
	return nil
}

// createEnvironmentFromCmd creates environment initialized with CLI input.
func createEnvironmentFromCmd(input cli.Input, imageUrl string, isWasmEnv bool) (*fv1.Environment, error) {
	e := utils.MultiErrorWithFormat()
	envName := input.String(flagkey.EnvName)
	envImg := imageUrl
	envNamespace := input.String(flagkey.NamespaceEnvironment)
	envBuildCmd := input.String(flagkey.EnvBuildcommand)
	envExternalNetwork := input.Bool(flagkey.EnvExternalNetwork)
	keepArchive := input.Bool(flagkey.EnvKeeparchive)
	envGracePeriod := input.Int64(flagkey.EnvGracePeriod)
	pullSecret := input.String(flagkey.EnvImagePullSecret)
	envVersion := input.Int(flagkey.EnvVersion)
	// Environment API interface version is not specified and
	// builder image is empty, set default interface version
	if envVersion == 0 {
		envVersion = 1
	}

	if input.IsSet(flagkey.EnvPoolsize) {
		// TODO: remove silently version 3 assignment, we need to warn user to set it explicitly.
		envVersion = 3
	}

	if !input.IsSet(flagkey.EnvPoolsize) {
		console.Info("poolsize setting default to 3")
	}
	poolsize := input.Int(flagkey.EnvPoolsize)
	if isWasmEnv {
		poolsize = 0
	}
	if poolsize < 1 {
		console.Warn("poolsize is not positive, if you are using pool manager please set positive value")
	}

	envBuilderImg := input.String(flagkey.EnvBuilderImage)
	if len(envBuilderImg) > 0 {
		if !input.IsSet(flagkey.EnvVersion) {
			// TODO: remove set env version to 2 silently, we need to warn user to set it explicitly.
			envVersion = 2
		}
		if len(envBuildCmd) == 0 {
			envBuildCmd = "build"
		}
	}

	builderEnvParams := input.StringSlice(flagkey.EnvBuilder)
	builderEnvList := util.GetEnvVarFromStringSlice(builderEnvParams)

	runtimeEnvParams := input.StringSlice(flagkey.EnvRuntime)
	runtimeEnvList := util.GetEnvVarFromStringSlice(runtimeEnvParams)

	resourceReq, err := util.GetResourceReqs(input, nil)
	if err != nil {
		e = multierror.Append(e, err)
	}

	if e.ErrorOrNil() != nil {
		return nil, e.ErrorOrNil()
	}

	annotations := map[string]string{}
	if checkWasmEnv(envName) {
		annotations["kubernetes.io/runtime-class"] = WasmRuntimeClass
	}
	env := &fv1.Environment{
		TypeMeta: metav1.TypeMeta{
			Kind:       fv1.CRD_NAME_ENVIRONMENT,
			APIVersion: fv1.CRD_VERSION,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        envName,
			Namespace:   envNamespace,
			Annotations: annotations,
		},
		Spec: fv1.EnvironmentSpec{
			Version: envVersion,
			Runtime: fv1.Runtime{
				Image: envImg,
				Container: &apiv1.Container{
					Env: runtimeEnvList,
				},
			},
			Builder: fv1.Builder{
				Image:   envBuilderImg,
				Command: envBuildCmd,
				Container: &apiv1.Container{
					Env: builderEnvList,
				},
			},
			Poolsize:                     poolsize,
			Resources:                    *resourceReq,
			AllowAccessToExternalNetwork: envExternalNetwork,
			TerminationGracePeriod:       envGracePeriod,
			KeepArchive:                  keepArchive,
			ImagePullSecret:              pullSecret,
		},
	}

	err = util.ApplyLabelsAndAnnotations(input, &env.ObjectMeta)
	if err != nil {
		return nil, err
	}
	err = env.Validate()
	if err != nil {
		return nil, fv1.AggregateValidationErrors("Environment", err)
	}

	return env, nil
}

func checkWasmEnv(envName string) bool {
	fmt.Printf("Checking if environment name %s has '-wasm' suffix...\n", envName)
	return strings.HasSuffix(envName, "-wasm")
}

func checkWasm(envImageName string) bool {
	isWasm := strings.HasSuffix(envImageName, ".wasm")
	fmt.Printf("checkWasm: Checking if %s is a wasm file -> %t\n", envImageName, isWasm)
	return isWasm
}
