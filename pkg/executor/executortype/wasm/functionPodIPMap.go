package wasm

import (
	"time"

	"go.uber.org/zap"

	"github.com/fission/fission/pkg/cache"
)

type (
	functionPodIPMap struct {
		logger *zap.Logger
		cache  *cache.Cache // map[functionuid]string
	}

)

func makeFunctionServiceMap(logger *zap.Logger, expiry time.Duration) *functionPodIPMap {
	return &functionPodIPMap{
		logger: logger.Named("function_podip_map"),
		cache:  cache.MakeCache(expiry, 0),
	}
}

// func keyFromMetadata(m *metav1.ObjectMeta) *metadataKey {
// 	return &metadataKey{
// 		Name:            m.Name,
// 		Namespace:       m.Namespace,
// 		ResourceVersion: m.ResourceVersion,
// 	}
// }

func (fmap *functionPodIPMap) lookup(fuid string) (string, error) {
	// mk := keyFromMetadata(f)
	item, err := fmap.cache.Get(fuid)
	if err != nil {
		return "", err
	}
	u := item.(string)
	return u, nil
}

func (fmap *functionPodIPMap) assign(fuid string, PodIP string) {
	// mk := keyFromMetadata(f)
	old, err := fmap.cache.Set(fuid, PodIP)
	if err != nil {
		if PodIP == old.(string) {
			return
		}
		fmap.logger.Error("error caching Pod IP for function with a different value", zap.Error(err))
		// ignore error
	}
}

func (fmap *functionPodIPMap) remove(fuid string) error {
	return fmap.cache.Delete(fuid)
}
