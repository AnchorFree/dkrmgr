package main

import (
	"net/http"
	"strconv"
	"strings"
)

// ShowMetrics parses app.database and outputs metrics for prometheus
func (app *App) ShowMetrics(w http.ResponseWriter, r *http.Request) {

	output := []string{"# TYPE docker_container_count gauge"}
	output = append(output, "docker_container_count{docker_container_count=\""+strconv.Itoa(len(app.database))+"\"} "+strconv.Itoa(len(app.database)))
	output = append(output, "# TYPE docker_container_healthy gauge")

	for name, container := range app.database {
		healthy := 0
		if container.Health != "" && container.Health != "healthy" && container.Health != "starting" {
			healthy = 1
		}
		if container.State != "running" {
			healthy = 1
		}
		output = append(output, "docker_container_healthy{image_id=\""+container.Image+"\",name=\""+name+"\"} "+strconv.Itoa(healthy))
	}

	output = append(output, "# TYPE docker_container_restart_count counter")
	for name, container := range app.database {
		output = append(output, "docker_container_restart_count{image_id=\""+container.Image+"\",name=\""+name+"\"} "+strconv.Itoa(container.Restarts))
	}
	output = append(output, "# TYPE docker_container_status gauge")
	for name, container := range app.database {
		output = append(output, "docker_container_status{image_id=\""+container.Image+"\",name=\""+name+"\",docker_container_status=\""+container.State+"\"} 1")
	}
	output = append(output, "# TYPE docker_container_stuck_inspect gauge")
	for name, container := range app.database {
		stuck := "0"
		if container.StuckInspect {
			stuck = "1"
		}
		output = append(output, "docker_container_stuck_inspect{image_id=\""+container.Image+"\",name=\""+name+"\"} "+stuck)
	}

	output = append(output, "# TYPE docker_container_healed counter")
	for name, container := range app.database {
		output = append(output, "docker_container_healed{image_id=\""+container.Image+"\",name=\""+name+"\",status=\"success\"} "+strconv.Itoa(container.Healed["success"]))
		output = append(output, "docker_container_healed{image_id=\""+container.Image+"\",name=\""+name+"\",status=\"fail\"} "+strconv.Itoa(container.Healed["fail"]))
	}

	output = append(output, "# TYPE docker_version gauge")
	output = append(output, "docker_version{docker_version=\""+app.dockerVersion["full"]+"\"} "+app.dockerVersion["stripped"])

	// And as a courtesy to the ancestor of docker-manager, docker-exporter, we include
	// the following two metrics. They are fake, because we do not compute them at all. However,
	// they were not working in docker-exporter neither.
	// We can make them work, as soon as the real intention behind these metrics
	// become clear.
	output = append(output, "# TYPE docker_longest_running gauge")
	output = append(output, "docker_longest_running{cmdline=\"nil\"} 0")

	output = append(output, "# TYPE docker_zombie_processes gauge")
	output = append(output, "docker_zombie_processes 0")

	w.Write([]byte(strings.Join(output, "\n")))

}
