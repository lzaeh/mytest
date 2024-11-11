编译打包过程
=================
- 先将代码包上传或者git fork一份到自身的代码仓库中
- 改造后的Fission即本项目使用goreleaser工具进行编译，并制作镜像上传至指定的镜像仓库。所有的Fission组件都会编译到一个名为fission-bundle的镜像中（也可以更改名称）

使用Goreleaser编译打包时，需要修改代码包中的goreleaser.yaml文件，如下所示：

```yaml
project_name: fission
release:
  github:
    owner: 1999huye1104    #改成自己的代码仓库用户名
    name: wasm-faas        #该项目的名称
  prerelease: true
  draft: false

before:
  hooks:
    - go mod tidy
snapshot:
  name_template: "{{ .Tag }}"
builds:
  - &build-linux
    id: fission-bundle
    ldflags:
      - -s -w
      - -X github.com/1999huye1104/wasm-faas/pkg/info.GitCommit={{.ShortCommit}}
      - -X github.com/1999huye1104/wasm-faas/pkg/info.BuildDate={{.Date}}
      - -X github.com/1999huye1104/wasm-faas/pkg/info.Version={{.Tag}}
    gcflags:
      - all=-trimpath={{ if index .Env "GITHUB_WORKSPACE"}}{{ .Env.GITHUB_WORKSPACE }}{{ else }}{{ .Env.PWD }}{{ end }}
    asmflags:
      - all=-trimpath={{ if index .Env "GITHUB_WORKSPACE"}}{{ .Env.GITHUB_WORKSPACE }}{{ else }}{{ .Env.PWD }}{{ end }}
    env:
      - CGO_ENABLED=0
    goos:
      - linux
    goarch:
      - amd64
      - arm64
      - arm
    goarm:
      - 7
    binary: fission-bundle 
    dir: ./cmd/fission-bundle
  - <<: *build-linux
    id: fission-cli
    goos: #此项为配置fission cli的编译版本可以选择linux、windows等
      - linux
      # - windows
      # - darwin
    goarch:
      - amd64
    binary: fission
    dir: ./cmd/fission-cli
    ignore:
      - goos: windows
        goarch: arm64
      - goos: darwin
        goarch: arm
        goarm: 7
      - goos: windows
        goarch: arm
        goarm: 7

dockers:
  - &docker-amd64
    use: buildx 
    goos: linux
    goarch: amd64
    ids:
      - fission-bundle
    image_templates:
      - "huyezs/fission-bundle:latest-amd64"  #此项需要改为制作完镜像上传的仓库地址
      - "huyezs/fission-bundle:{{ .Tag }}-amd64" #此项需要将huyezs/fission-bundle改为制作完镜像上传的仓库地址
    dockerfile: cmd/fission-bundle/Dockerfile
    build_flag_templates:
      - "--platform=linux/amd64"

docker_manifests:
  - name_template: huyezs/fission-bundle:{{ .Tag }}
    image_templates:
      - huyezs/fission-bundle:{{ .Tag }}-amd64
changelog:
  skip: false
archives:
  - id: fission
    builds:
      - fission-cli
    name_template: "{{ .ProjectName }}-{{ .Tag }}-{{ .Os }}-{{ .Arch }}"
    format: binary
checksum:
  name_template: "checksums.txt"
  algorithm: sha256

```

- 安装完goreleaser并保证可用后，进行编译工作，如下例所示：

```bash
#按照上一步要求修改goreleaser.yaml文件
$ git add .

$ git commit -m "xxxxx"

$ git tag -a <你的tag> -m "xxxxxxx"

$ git push origin <你的tag> 

#以上为为当前代码创建tag，然后推送到远程
#下面在goreleaser.yaml文件路径上，使用goreleaser编译打包
$ goreleaser --clean

#成功完成以上就可以在镜像仓库中获取fission-bundle镜像，在代码仓库发布的该tag的Assets中有fission cli可执行程序
```



部署过程
=================

```bash
//创建CRD资源
kubectl create -k "github.com/fission/fission/crds/v1?ref=v1.17.0"

//设置默认当前namespace为fission
export FISSION_NAMESPACE="fission"
kubectl create namespace $FISSION_NAMESPACE
kubectl config set-context --current --namespace=$FISSION_NAMESPACE

//先获取yaml文件
git clone https://github.com/1999huye1104/newyaml.git

//安装fission
cd newyaml
kubectl apply -f all.yaml

//CLI安装,如果是自己编译后的版本可以将url改为自己编译发布的CLI
curl -Lo faas-cli https://github.com/1999huye1104/wasm-faas/releases/download/v1.18.14/fission-v1.18.14-linux-amd64 && chmod +x faas-cli && sudo mv faas-cli /usr/local/bin/
```



注意事项
=================

- 在使用goreleaser编译制作镜像时，本地docker要配置daemon.json，加入以下一项：

```yaml
"experimental": true,
```



