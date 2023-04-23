package function

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
	apiv1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	
	_package "github.com/fission/fission/pkg/fission-cli/cmd/package"
	fv1 "github.com/fission/fission/pkg/apis/core/v1"
	ferror "github.com/fission/fission/pkg/error"
	"github.com/fission/fission/pkg/fission-cli/cliwrapper/cli"
	"github.com/fission/fission/pkg/fission-cli/cmd"
	"github.com/fission/fission/pkg/fission-cli/cmd/spec"
	"github.com/fission/fission/pkg/fission-cli/console"
	flagkey "github.com/fission/fission/pkg/fission-cli/flag/key"
	"github.com/fission/fission/pkg/fission-cli/util"
)

type RunWasmSubCommand struct {
	cmd.CommandActioner
	function *fv1.Function
	specFile string
}

func RunWasm(input cli.Input) error {
	return (&RunWasmSubCommand{}).do(input)
}

func (opts *RunWasmSubCommand) do(input cli.Input) error {
	err := opts.complete(input)
	if err != nil {
		return err
	}
	return opts.run(input)
}

func (opts *RunWasmSubCommand) complete(input cli.Input) error {
	fnName := input.String(flagkey.FnName)
	fnNamespace := input.String(flagkey.NamespaceFunction)

	// user wants a spec, create a yaml file with package and function
	toSpec := false
	if input.Bool(flagkey.SpecSave) {
		toSpec = true
		opts.specFile = fmt.Sprintf("function-%v.yaml", fnName)
	}
	specDir := util.GetSpecDir(input)
	specIgnore := util.GetSpecIgnore(input)

	if !toSpec {
		// check for unique function names within a namespace
		fn, err := opts.Client().V1().Function().Get(&metav1.ObjectMeta{
			Name:      input.String(flagkey.FnName),
			Namespace: input.String(flagkey.NamespaceFunction),
		})
		if err != nil && !ferror.IsNotFound(err) {
			return err
		} else if fn != nil {
			return errors.New("a function with the same name already exists")
		}
	}

	entrypoint := input.String(flagkey.FnEntrypoint)

	fnTimeout := input.Int(flagkey.FnExecutionTimeout)
	if fnTimeout <= 0 {
		return errors.Errorf("--%v must be greater than 0", flagkey.FnExecutionTimeout)
	}

	fnIdleTimeout := input.Int(flagkey.FnIdleTimeout)
	pkgName := input.String(flagkey.FnPackageName)

	secretNames := input.StringSlice(flagkey.FnSecret)
	cfgMapNames := input.StringSlice(flagkey.FnCfgMap)

	es, err := getExecutionStrategy(fv1.ExecutorTypeWasm, input)
	if err != nil {
		return err
	}
	invokeStrategy := &fv1.InvokeStrategy{
		ExecutionStrategy: *es,
		StrategyType:      fv1.StrategyTypeExecution,
	}
	resourceReq, err := util.GetResourceReqs(input, &apiv1.ResourceRequirements{})
	if err != nil {
		return err
	}

	fnGracePeriod := input.Int64(flagkey.FnGracePeriod)
	if fnGracePeriod < 0 {
		console.Warn("grace period must be a non-negative integer, using default value (6 mins)")
	}

    //以下是为wasm文件创建package和archive
	var pkgMetadata *metav1.ObjectMeta
	envName:="wasm"
	envNamespace:="default"
	var pkg *fv1.Package

	if len(pkgName) > 0 {
		
		if toSpec {
			fr, err := spec.ReadSpecs(specDir, specIgnore, false)
			if err != nil {
				return errors.Wrap(err, fmt.Sprintf("error reading spec in '%v'", specDir))
			}
			obj := fr.SpecExists(&fv1.Package{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pkgName,
					Namespace: fnNamespace,
				},
			}, true, false)
			if obj == nil {
				return errors.Errorf("please create package %v spec file before referencing it", pkgName)
			}
			pkg = obj.(*fv1.Package)
			pkgMetadata = &pkg.ObjectMeta
		} else {
			// use existing package
			pkg, err = opts.Client().V1().Package().Get(&metav1.ObjectMeta{
				Namespace: fnNamespace,
				Name:      pkgName,
			})
			_, err = opts.Client().V1().Package().Get(&metav1.ObjectMeta{
				Namespace: fnNamespace,
				Name:      pkgName,
			})

			if err != nil {
				return errors.Wrap(err, fmt.Sprintf("read package in '%v' in Namespace: %s. Package needs to be present in the same namespace as function", pkgName, fnNamespace))
			}
			pkgMetadata = &pkg.ObjectMeta
		}

		// envName = pkg.Spec.Environment.Name
		// if envName != input.String(flagkey.FnEnvironmentName) {
		// 	console.Warn("Function's environment is different than package's environment, package's environment will be used for creating function")
		// }
		// envNamespace = pkg.Spec.Environment.Namespace
	} else {
		// need to specify environment for creating new package
		// envName = input.String(flagkey.FnEnvironmentName)
		// if len(envName) == 0 {
		// 	return errors.New("need --env argument")
		// }

		// if toSpec {
		// 	fr, err := spec.ReadSpecs(specDir, specIgnore, false)
		// 	if err != nil {
		// 		return errors.Wrap(err, fmt.Sprintf("error reading spec in '%v'", specDir))
		// 	}
		// 	exists, err := fr.ExistsInSpecs(fv1.Environment{
		// 		ObjectMeta: metav1.ObjectMeta{
		// 			Name:      envName,
		// 			Namespace: envNamespace,
		// 		},
		// 	})
		// 	if err != nil {
		// 		return err
		// 	}
		// 	if !exists {
		// 		console.Warn(fmt.Sprintf("Function '%v' references unknown Environment '%v', please create it before applying spec",
		// 			fnName, envName))
		// 	}
		// } else {
		// 	_, err := opts.Client().V1().Environment().Get(&metav1.ObjectMeta{
		// 		Namespace: envNamespace,
		// 		Name:      envName,
		// 	})
		// 	if err != nil {
		// 		if e, ok := err.(ferror.Error); ok && e.Code == ferror.ErrorNotFound {
		// 			console.Warn(fmt.Sprintf("Environment \"%v\" does not exist. Please create the environment before executing the function. \nFor example: `fission env create --name %v --envns %v --image <image>`\n", envName, envName, envNamespace))
		// 		} else {
		// 			return errors.Wrap(err, "error retrieving environment information")
		// 		}
		// 	}
		// }
        
		srcArchiveFiles := input.StringSlice(flagkey.PkgSrcArchive)
		var deployArchiveFiles []string
		//不将wasm文件打包为zip
		noZip := true
		code := input.String(flagkey.PkgCode)
		if len(code) == 0 {
			deployArchiveFiles = input.StringSlice(flagkey.PkgDeployArchive)
		} else {
			deployArchiveFiles = append(deployArchiveFiles, input.String(flagkey.PkgCode))
			noZip = true
		}
		// return error when both src & deploy archive are empty
		if len(srcArchiveFiles) == 0 && len(deployArchiveFiles) == 0 {
			return errors.New("need --code or --deploy  argument")
		}

		buildcmd := input.String(flagkey.PkgBuildCmd)
		id, err := uuid.NewV4()
		if err != nil {
			return errors.Wrap(err, "error generating uuid")
		}
		pkgName := generatePackageName(fnName, id.String())

		// create new package in the same namespace as the function.
		// pkgMetadata, err = _package.CreateFakePackage(input, opts.Client(), pkgName, fnNamespace, envName, envNamespace,
		// 	srcArchiveFiles, deployArchiveFiles, buildcmd, specDir, opts.specFile, noZip)
		pkg, err = _package.CreateWasmPackage(input, opts.Client(), pkgName, fnNamespace, envName, envNamespace,
		srcArchiveFiles, deployArchiveFiles, buildcmd, specDir, opts.specFile, noZip)
		if err != nil {
			return errors.Wrap(err, "error creating package")
		}
	}
	//以上wasm文件创建package和archive完成


	var port int
	var command, args string

	
	port = input.Int(flagkey.FnPort)
	// command = input.String(flagkey.FnCommand)
	args = input.String(flagkey.FnArgs)

	var secrets []fv1.SecretReference
	var cfgmaps []fv1.ConfigMapReference

	if len(secretNames) > 0 {
		// check the referenced secret is in the same ns as the function, if not give a warning.
		if !toSpec { // TODO: workaround in order not to block users from creating function spec, remove it.
			for _, secretName := range secretNames {
				err := opts.Client().V1().Misc().SecretExists(&metav1.ObjectMeta{
					Namespace: fnNamespace,
					Name:      secretName,
				})
				if err != nil {
					if k8serrors.IsNotFound(err) {
						console.Warn(fmt.Sprintf("Secret %s not found in Namespace: %s. Secret needs to be present in the same namespace as function", secretName, fnNamespace))
					} else {
						return errors.Wrapf(err, "error checking secret %s", secretName)
					}
				}
			}
		}
		for _, secretName := range secretNames {
			newSecret := fv1.SecretReference{
				Name:      secretName,
				Namespace: fnNamespace,
			}
			secrets = append(secrets, newSecret)
		}
	}

	if len(cfgMapNames) > 0 {
		// check the referenced cfgmap is in the same ns as the function, if not give a warning.
		if !toSpec {
			for _, cfgMapName := range cfgMapNames {
				err := opts.Client().V1().Misc().ConfigMapExists(&metav1.ObjectMeta{
					Namespace: fnNamespace,
					Name:      cfgMapName,
				})
				if err != nil {
					if k8serrors.IsNotFound(err) {
						console.Warn(fmt.Sprintf("ConfigMap %s not found in Namespace: %s. ConfigMap needs to be present in the same namespace as function", cfgMapName, fnNamespace))
					} else {
						return errors.Wrapf(err, "error checking configmap %s", cfgMapName)
					}
				}
			}
		}
		for _, cfgMapName := range cfgMapNames {
			newCfgMap := fv1.ConfigMapReference{
				Name:      cfgMapName,
				Namespace: fnNamespace,
			}
			cfgmaps = append(cfgmaps, newCfgMap)
		}
	}

	opts.function = &fv1.Function{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fnName,
			Namespace: fnNamespace,
		},
		Spec: fv1.FunctionSpec{
			Secrets:         secrets,
			ConfigMaps:      cfgmaps,
			Resources:       *resourceReq,
			InvokeStrategy:  *invokeStrategy,
			FunctionTimeout: fnTimeout,
			IdleTimeout:     &fnIdleTimeout,
		},
	}

	err = util.ApplyLabelsAndAnnotations(input, &opts.function.ObjectMeta)
	if err != nil {
		return err
	}

	//加上wasm模块必须的annotation
	urlAnnotation:=pkg.Spec.Deployment.URL
	nameAnnotation:=pkg.Name+".wasm"
    opts.function.ObjectMeta.Annotations["wasm.module.url"]=urlAnnotation
	opts.function.ObjectMeta.Annotations["wasm.module.filename"]=nameAnnotation
    
	container := &apiv1.Container{
		Name:  fnName,
		Image: fnName,
		Ports: []apiv1.ContainerPort{
			{
				Name:          "http-env",
				ContainerPort: int32(port),
			},
		},
	}
	command=nameAnnotation
	if command != "" {
		container.Command = strings.Split(command, " ")
	}
	if args != "" {
		container.Args = strings.Split(args, " ")
	}

    opts.function.Spec.Environment = fv1.EnvironmentReference{
		Name:      "wasm",
		Namespace: "default",
	}

	opts.function.Spec.Package = fv1.FunctionPackageRef{
		FunctionName: entrypoint,
		PackageRef: fv1.PackageRef{
			Namespace:       pkgMetadata.Namespace,
			Name:            pkgMetadata.Name,
			ResourceVersion: pkgMetadata.ResourceVersion,
		},
	}
    // 以下为测试版本 正式版在上面 

	// opts.function.Spec.Package = fv1.FunctionPackageRef{
	// 	FunctionName: entrypoint,
	// 	PackageRef: fv1.PackageRef{
	// 		Namespace:       fnNamespace,
	// 		Name:            fnName,
	// 		// ResourceVersion: pkgMetadata.ResourceVersion,
	// 	},
	// }

	opts.function.Spec.PodSpec = &apiv1.PodSpec{
		Containers:                    []apiv1.Container{*container},
		TerminationGracePeriodSeconds: &fnGracePeriod,
	}
   
	return nil
}

// run write the resource to a spec file or create a fission CRD with remote fission server.
// It also prints warning/error if necessary.
func (opts *RunWasmSubCommand) run(input cli.Input) error {
	// if we're writing a spec, don't create the function
	// save to spec file or display the spec to console
	if input.Bool(flagkey.SpecDry) {
		return spec.SpecDry(*opts.function)
	}

	if input.Bool(flagkey.SpecSave) {
		err := spec.SpecSave(*opts.function, opts.specFile)
		if err != nil {
			return errors.Wrap(err, "error saving function spec")
		}
		return nil
	}

	_, err := opts.Client().V1().Function().Create(opts.function)
	if err != nil {
		return errors.Wrap(err, "error creating function")
	}

	fmt.Printf("function '%v' created\n", opts.function.ObjectMeta.Name)
	return nil
}
