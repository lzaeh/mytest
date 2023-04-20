package _package

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/dchest/uniuri"
	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fv1 "github.com/fission/fission/pkg/apis/core/v1"
	"github.com/fission/fission/pkg/controller/client"
	"github.com/fission/fission/pkg/fission-cli/cliwrapper/cli"
	// "github.com/fission/fission/pkg/fission-cli/cmd"
	"github.com/fission/fission/pkg/fission-cli/cmd/spec"
	// "github.com/fission/fission/pkg/fission-cli/console"
	flagkey "github.com/fission/fission/pkg/fission-cli/flag/key"
	"github.com/fission/fission/pkg/fission-cli/util"
)

func CreateFakePackage(input cli.Input, client client.Interface, pkgName string, pkgNamespace string, envName string, envNamespace string,
	srcArchiveFiles []string, deployArchiveFiles []string, buildcmd string, specDir string, specFile string, noZip bool) (*metav1.ObjectMeta, error) {

	insecure := input.Bool(flagkey.PkgInsecure)
	deployChecksum := input.String(flagkey.PkgDeployChecksum)
	srcChecksum := input.String(flagkey.PkgSrcChecksum)

	pkgSpec := fv1.PackageSpec{
		Environment: fv1.EnvironmentReference{
			Namespace: envNamespace,
			Name:      envName,
		},
	}
	var pkgStatus fv1.BuildStatus = fv1.BuildStatusSucceeded

	if len(deployArchiveFiles) > 0 {
		if len(specFile) > 0 { // we should do this in all cases, i think
			pkgStatus = fv1.BuildStatusNone
		}
		deployment, err := CreateArchive(client, input, deployArchiveFiles, noZip, insecure, deployChecksum, specDir, specFile)
		if err != nil {
			return nil, errors.Wrap(err, "error creating source archive")
		}
		pkgSpec.Deployment = *deployment
		if len(pkgName) == 0 {
			pkgName = util.KubifyName(fmt.Sprintf("%v-%v", path.Base(deployArchiveFiles[0]), uniuri.NewLen(4)))
		}
	}
	if len(srcArchiveFiles) > 0 {
		source, err := CreateArchive(client, input, srcArchiveFiles, false, insecure, srcChecksum, specDir, specFile)
		if err != nil {
			return nil, errors.Wrap(err, "error creating deploy archive")
		}
		pkgSpec.Source = *source
		pkgStatus = fv1.BuildStatusPending // set package build status to pending
		if len(pkgName) == 0 {
			pkgName = util.KubifyName(fmt.Sprintf("%v-%v", path.Base(srcArchiveFiles[0]), uniuri.NewLen(4)))
		}
	}

	if len(buildcmd) > 0 {
		pkgSpec.BuildCommand = buildcmd
	}

	if len(pkgName) == 0 {
		id, err := uuid.NewV4()
		if err != nil {
			return nil, errors.Wrap(err, "error generating UUID")
		}
		pkgName = strings.ToLower(id.String())
	}

	pkg := &fv1.Package{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pkgName,
			Namespace: pkgNamespace,
		},
		Spec: pkgSpec,
		Status: fv1.PackageStatus{
			BuildStatus:         pkgStatus,
			LastUpdateTimestamp: metav1.Time{Time: time.Now().UTC()},
		},
	}

	if input.Bool(flagkey.SpecDry) {
		return &pkg.ObjectMeta, spec.SpecDry(*pkg)
	}

	if input.Bool(flagkey.SpecSave) {
		// if a package with the same spec exists, don't create a new spec file
		fr, err := spec.ReadSpecs(util.GetSpecDir(input), util.GetSpecIgnore(input), false)
		if err != nil {
			return nil, errors.Wrap(err, "error reading specs")
		}

		obj := fr.SpecExists(pkg, true, true)
		if obj != nil {
			pkg := obj.(*fv1.Package)
			fmt.Printf("Re-using previously created package %v\n", pkg.ObjectMeta.Name)
			return &pkg.ObjectMeta, nil
		}

		err = spec.SpecSave(*pkg, specFile)
		if err != nil {
			return nil, errors.Wrap(err, "error saving package spec")
		}
		return &pkg.ObjectMeta, nil
	} else {
		pkgMetadata, err := client.V1().Package().Create(pkg)
		if err != nil {
			return nil, errors.Wrap(err, "error creating package")
		}
		// fmt.Printf("Package '%v' created\n", pkgMetadata.GetName())
		return pkgMetadata, nil
	}
}
