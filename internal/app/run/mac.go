package run

import (
	"fmt"
	"strconv"
	"time"

	"github.com/nektos/act/pkg/artifactcache"
)

func (r *Runner) computeCacheServerUrl(repo, run string) {
	key := r.cfg.Cache.Secret
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := artifactcache.ComputeMac(key, repo, run, ts)
	baseURL := r.envs["ACTIONS_CACHE_URL"]
	r.envs["ACTIONS_CACHE_URL"] = fmt.Sprintf("%s/%s/%s/%s/%s/", baseURL, repo, run, ts, mac)
}
