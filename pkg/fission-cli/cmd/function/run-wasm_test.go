package function

import (
	// "fmt"
	"fmt"
	"testing"

	fv1 "github.com/fission/fission/pkg/apis/core/v1"
	"github.com/fission/fission/pkg/controller/client"
	"github.com/fission/fission/pkg/controller/client/rest"
	"github.com/fission/fission/pkg/fission-cli/cliwrapper/driver/dummy"
	"github.com/fission/fission/pkg/fission-cli/cmd"
	flagkey "github.com/fission/fission/pkg/fission-cli/flag/key"
)

type examp struct {
	name                   string
	testArgs               map[string]interface{}
	existingInvokeStrategy *fv1.InvokeStrategy
	expectedResult         *fv1.InvokeStrategy
	expectError            bool

}

func TestRunwasmComplete(t *testing.T) {
	serverUrl:= "http://127.0.0.1:8888"
	restClient := rest.NewRESTClient(serverUrl)
	cmd.SetClientset(client.MakeFakeClientset(restClient))
    
	runWasm:=&RunWasmSubCommand{}
	ex := &examp {
		name:                   "use run-wasm to creat a webaasembly function",
		testArgs:               map[string]interface{}{
			flagkey.FnName:                      "WASM function",
			flagkey.NamespaceFunction:        "wasm-function-ns",
			flagkey.PkgCode:                 "test.go",
			flagkey.FnExecutionTimeout:                       50,
			flagkey.FnGracePeriod:                     int64(50),
		},
	}
	flags := dummy.TestFlagSet()

	for k, v := range ex.testArgs {
		flags.Set(k, v)
	}

	err:=runWasm.complete(flags)
	if err!=nil{
		fmt.Print(err)
		t.Log("创建失败")
	}
	t.Log("创建成功")
}