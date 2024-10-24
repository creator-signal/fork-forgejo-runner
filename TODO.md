# TODO for proxy hardening

- [ ] build the cache proxy
  - [ ] Actually build it
  - [ ] find out how to add it to the workflow container network
- [ ] design the authentication data
- [ ] modify the real proxy to only accept auth data
- [ ] figure out how to store the caches per repo
- [ ] modify the cache URL injected into workflows

- maybe it would be worth it to set up a cache provider interface that the server and proxy both implement?


## Authentication model

- Cache key
  - 512 bits random key
  - stored in the config of the cache server and proxy
- We are just trying to prove that the proxy has access to the canonical repository name
- When the proxy is started, it is given the canonical name of the repository it's running for
- Whenever the proxy forwards a request to the cache server, it injects the canonical name of the repository, and a proof that it is the cache server
- The proof is the canonical name of the repository, HMAC'ed with the cache key
- On the other end to verify wether to allow the request the cache server verifies the proof by HMACing the received repo name with its own copy of the cache secret. If it matches the request is allowed.
