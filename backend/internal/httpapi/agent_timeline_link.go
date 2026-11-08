package httpapi

func AgentTimelineLink(projectID, jobID string) *string {
	if projectID == "" || jobID == "" {
		return nil
	}
	link := "/api/v1/projects/" + projectID + "/jobs/" + jobID + "/agent-timeline"
	return &link
}
