package main

import (
	"net/http"
	"strconv"
	"strings"
)

// ShowMetrics parses app.database and outputs metrics for prometheus
func (app *App) ShowMetrics(w http.ResponseWriter, r *http.Request) {

	var output strings.Builder
	defer app.mutex.RUnlock()
	app.mutex.Lock()
	output.Grow(4096) // Let's allocate 4kb right away, most probably we'll use it.
	output.WriteString("# TYPE docker_container_count gauge\n")
	output.WriteString("docker_container_count{docker_container_count=\"" + strconv.Itoa(len(app.database)) + "\"} " + strconv.Itoa(len(app.database)) + "\n")
	output.WriteString("# TYPE docker_container_healthy gauge\n")

	for name, container := range app.database {
		healthy := "0\n"
		if container.Health == "unhealthy" || container.State != "running" {
			healthy = "1\n"
		}
		output.WriteString("docker_container_healthy{image_id=\"" + container.Image + "\",name=\"" + name + "\"} " + healthy)
	}

	output.WriteString("# TYPE docker_container_restart_count counter\n")
	for name, container := range app.database {
		output.WriteString("docker_container_restart_count{image_id=\"" + container.Image + "\",name=\"" + name + "\"} " + strconv.Itoa(container.Restarts) + "\n")
	}
	output.WriteString("# TYPE docker_container_status gauge\n")
	for name, container := range app.database {
		output.WriteString("docker_container_status{image_id=\"" + container.Image + "\",name=\"" + name + "\",docker_container_status=\"" + container.State + "\"} 1\n")
	}
	output.WriteString("# TYPE docker_container_stuck_inspect gauge\n")
	for name, container := range app.database {
		stuck := "0\n"
		if container.StuckInspect {
			stuck = "1\n"
		}
		output.WriteString("docker_container_stuck_inspect{image_id=\"" + container.Image + "\",name=\"" + name + "\"} " + stuck)
	}

	output.WriteString("# TYPE docker_container_healed counter\n")
	for name, container := range app.database {
		output.WriteString("docker_container_healed{image_id=\"" + container.Image + "\",name=\"" + name + "\",status=\"success\"} " + strconv.Itoa(container.Healed["success"]) + "\n")
		output.WriteString("docker_container_healed{image_id=\"" + container.Image + "\",name=\"" + name + "\",status=\"fail\"} " + strconv.Itoa(container.Healed["fail"]) + "\n")
	}

	output.WriteString("# TYPE docker_version gauge\n")
	output.WriteString("docker_version{docker_version=\"" + app.dockerVersion["full"] + "\"} " + app.dockerVersion["stripped"] + "\n")
	w.Write([]byte(output.String()))

}
