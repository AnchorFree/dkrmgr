DKRMGR -- manager for docker containers
=======================================

Description
-----------

DKRMGR is a go daemon to export docker containers metrics for Prometheus.
DKRMGR also includes healing mode — if a container has a healthcheck defined
and container health is `unhealthy` DKRMGR schedules restart for the container.
If the container stays `unhealthy` after the restart, DKRMGR schedules another
restart with a longer delay before the restart.


Configuration
-------------

DKRMGR is configured with environment variables:

* **DKRMGR_DOCKER_SOCKET**  
Path to the docker socket. Default is **/var/run/docker.sock**

* **DKRMGR_SCRAPE_INTERVAL**  
Interval between calls to docker API to get containers state changes. Default is **10s**

* **DKRMGR_SCRAPE_TIMEOUT**  
Timeout for the container inspect API call. Default is **5s**

* **DKRMGR_CLEANUP_INTERVAL**  
Interval between runs of garbage collector to remove healed/vanished patients
from the list. Default is **10s**

* **DKRMGR_RESTART_BACKOFF_MIN**  
Minimum delay before restarting a container. Default is **5s**

* **DKRMGR_RESTART_BACKOFF_MAX**  
Maximum delay before restarting a container. Default is **5m**.
Each consecutive restart (assuming the container stays unhealthy) will
be scheduled with a delay of (lastDelay*2), so, with default values this
will be 5s, 10s, 20s et caetera. However, when delay reaches maximum, it
will stay there.

* **DKRMGR_HEAL_MODE**  
Turns on healing mode. Default is **false**.

* **DKRMGR_PORT**  
Port on which to start http server to serve metrics. Default is **9102**.
Metrics will be available at `http://localhost:9102/metrics`.

* **DKRMGR_DEBUG_MODE**  
Turns on debug level logging. Default is **false**.

Exported metrics
----------------

| Name | Type | Labels | Remarks |
| ---- | ---- | ------ | ------- |
| docker_container_count | gauge | docker_container_count | |
| docker_container_healthy | gauge | name, image_id | 0 if healthy, 1 otherwise |
| docker_container_restart_count | counter | name, image_id | |
| docker_container_status | gauge | name, image_id, docker_container_status | |
| docker_container_stuck_inspect | gauge | name, image_id | 0 if ok, 1 if inspect failed |
| docker_container_healed | counter | name, image_id, success, fail | |

