package main

import (
	"context"
	"fmt"
	"strings"
)

// scoutNativeAction is a direct, authenticated Stride operation. It is
// deliberately separate from scoutRouterProposal: native UI work executes
// through the same service seam as the manual control and never launches an
// agent thread or goal workflow.
type scoutNativeAction struct {
	ToolID string
	Fields map[string]string
}

type scoutNativeActionSpec struct {
	ID          string
	Description string
	Required    []string
	Allowed     []string
}

var scoutNativeActionSpecs = []scoutNativeActionSpec{
	{ID: "control_app", Description: "Open a Stride surface. fields: tool (office, room, chat, artifacts, research, design, grill, board, memory, files), optional artifact_id.", Required: []string{"tool"}, Allowed: []string{"tool", "artifact_id", "also_open"}},
	{ID: "create_ticket", Description: "Create a Board card. fields: title, notes, owner, status, optional tags, due_date, and key_dates as comma-separated text.", Required: []string{"title"}, Allowed: []string{"title", "notes", "owner", "status", "tags", "due_date", "key_dates"}},
	{ID: "move_ticket", Description: "Move a Board card. fields: card_id or exact title, status.", Required: []string{"status"}, Allowed: []string{"card_id", "title", "card_title", "status"}},
	{ID: "update_ticket", Description: "Edit a Board card. fields: card_id or exact card_title, plus title, notes, owner, status, tags, due_date, key_dates, or replace_key_dates.", Required: nil, Allowed: []string{"card_id", "card_title", "title", "notes", "owner", "status", "tags", "due_date", "key_dates", "replace_key_dates"}},
	{ID: "delete_ticket", Description: "Delete a Board card. fields: card_id or exact title.", Required: nil, Allowed: []string{"card_id", "title", "card_title"}},
	{ID: "undo_delete_ticket", Description: "Restore the most recently deleted Board card.", Required: nil, Allowed: nil},
	{ID: "create_channel", Description: "Create a public Bonfire Chat channel. fields: name.", Required: []string{"name"}, Allowed: []string{"name"}},
	{ID: "archive_channel", Description: "Archive an existing Bonfire Chat channel by exact visible name. fields: channel.", Required: []string{"channel"}, Allowed: []string{"channel"}},
	{ID: "rename_channel", Description: "Rename an existing Bonfire Chat channel. fields: channel, new_name.", Required: []string{"channel", "new_name"}, Allowed: []string{"channel", "new_name"}},
	{ID: "post_to_channel", Description: "Post the user's exact message to a public channel. The main channel, main chat, or pinned Bonfire chat means the permanent Bonfire Chat. fields: channel, text, optional mention.", Required: []string{"channel", "text"}, Allowed: []string{"channel", "text", "mention"}},
	{ID: "create_file_folder", Description: "Create a Drive folder. fields: name.", Required: []string{"name"}, Allowed: []string{"name"}},
	{ID: "rename_file_folder", Description: "Rename a Drive folder by exact visible name. fields: folder, new_name.", Required: []string{"folder", "new_name"}, Allowed: []string{"folder", "new_name"}},
	{ID: "delete_file_folder", Description: "Delete a Drive folder label; its files return to All files. fields: folder.", Required: []string{"folder"}, Allowed: []string{"folder"}},
	{ID: "delete_file", Description: "Delete or remove one Drive file by exact visible name through its source-aware delete seam. fields: file.", Required: []string{"file"}, Allowed: []string{"file"}},
	{ID: "organize_files", Description: "Put visible files into a Drive folder. fields: folderName, optional fileNames as comma-separated name fragments.", Required: []string{"folderName"}, Allowed: []string{"folderName", "fileNames", "createIfMissing"}},
	{ID: "save_to_files", Description: "Save finished deliverables to Drive. fields: fileNames as comma-separated title fragments, optional folderName.", Required: []string{"fileNames"}, Allowed: []string{"fileNames", "folderName"}},
	{ID: "send_notification", Description: "Create a Stride notification. fields: text, kind (info, task, agent, chat, alert), audience (me or everyone), optional tool and deliver.", Required: []string{"text", "kind", "audience"}, Allowed: []string{"text", "kind", "audience", "tool", "deliver"}},
}

// privateScoutNativeToolDefinitions are requester-dependent controls that must
// never leak into the shared-room Realtime session (which has no single actor).
// Typed @Scout actions use the same executors below with the authenticated
// message author as principal.
func privateScoutNativeToolDefinitions() []map[string]any {
	stringProperty := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []map[string]any{
		{"type": "function", "name": "archive_channel", "description": "Archive an existing Bonfire Chat channel the signed-in requester may archive.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"channel": stringProperty("Exact visible channel name; a leading # is tolerated.")}, "required": []string{"channel"}, "additionalProperties": false}},
		{"type": "function", "name": "rename_channel", "description": "Rename an existing Bonfire Chat channel the signed-in requester may edit.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"channel": stringProperty("Exact current channel name."), "new_name": stringProperty("New channel name.")}, "required": []string{"channel", "new_name"}, "additionalProperties": false}},
		{"type": "function", "name": "create_file_folder", "description": "Create a folder on the signed-in requester's Drive surface.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"name": stringProperty("Folder name.")}, "required": []string{"name"}, "additionalProperties": false}},
		{"type": "function", "name": "rename_file_folder", "description": "Rename a Drive folder the signed-in requester manages.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"folder": stringProperty("Exact current folder name."), "new_name": stringProperty("New folder name.")}, "required": []string{"folder", "new_name"}, "additionalProperties": false}},
		{"type": "function", "name": "delete_file_folder", "description": "Delete a Drive folder label the signed-in requester manages. Files return to All files and are not deleted.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"folder": stringProperty("Exact folder name.")}, "required": []string{"folder"}, "additionalProperties": false}},
		{"type": "function", "name": "delete_file", "description": "Delete or remove one writable Drive file through its source-aware delete seam.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"file": stringProperty("Exact visible file name.")}, "required": []string{"file"}, "additionalProperties": false}},
	}
}

func scoutNativeActionInstructions() string {
	lines := make([]string, 0, len(scoutNativeActionSpecs))
	for _, spec := range scoutNativeActionSpecs {
		lines = append(lines, spec.ID+": "+spec.Description)
	}
	return strings.Join(lines, "\n")
}

// scoutMessageMayRequestNativeAction is the cheap public-channel gate. A
// normal @Scout question should still make one answer-model call, not pay for a
// router turn first; only action-shaped messages enter the native classifier.
// Private Scout always gets the classifier because short confirmations rely on
// its recent history.
func scoutMessageMayRequestNativeAction(text string) bool {
	lower := strings.ToLower(text)
	verb := false
	for _, token := range []string{"open", "create", "add", "delete", "remove", "archive", "rename", "move", "update", "change", "post", "send", "notify", "save", "organize", "restore", "undo"} {
		if strings.Contains(lower, token) {
			verb = true
			break
		}
	}
	if !verb {
		return false
	}
	for _, object := range []string{"board", "project", "card", "ticket", "channel", "folder", "file", "drive", "notification", "bonfire map", " it", "that"} {
		if strings.Contains(lower, object) {
			return true
		}
	}
	return false
}

func scoutNativeActionSpecByID(id string) (scoutNativeActionSpec, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	for _, spec := range scoutNativeActionSpecs {
		if spec.ID == id {
			return spec, true
		}
	}
	return scoutNativeActionSpec{}, false
}

func scoutNativeActionFromRouter(output openAIScoutRouterOutput) (*scoutNativeAction, error) {
	spec, ok := scoutNativeActionSpecByID(output.ToolID)
	if !ok {
		return nil, fmt.Errorf("unknown Scout app action")
	}
	fields := make(map[string]string, len(output.Fields))
	allowed := make(map[string]struct{}, len(spec.Allowed))
	for _, key := range spec.Allowed {
		allowed[key] = struct{}{}
	}
	for _, field := range output.Fields {
		key := strings.TrimSpace(field.Key)
		value := strings.TrimSpace(field.Value)
		if key == "" || value == "" {
			continue
		}
		if _, ok := allowed[key]; !ok {
			continue
		}
		fields[key] = value
	}
	for _, required := range spec.Required {
		if strings.TrimSpace(fields[required]) == "" {
			return nil, fmt.Errorf("Scout app action %s requires %s", spec.ID, required)
		}
	}
	return &scoutNativeAction{ToolID: spec.ID, Fields: fields}, nil
}

func scoutNativeActionArgs(fields map[string]string) map[string]any {
	args := make(map[string]any, len(fields))
	for key, value := range fields {
		switch key {
		case "tags", "labels", "fileNames", "also_open":
			parts := strings.Split(value, ",")
			items := make([]string, 0, len(parts))
			for _, part := range parts {
				if item := strings.TrimSpace(part); item != "" {
					items = append(items, item)
				}
			}
			args[key] = items
		case "remove_all", "replace_key_dates", "createIfMissing":
			args[key] = strings.EqualFold(strings.TrimSpace(value), "true")
		default:
			args[key] = value
		}
	}
	return args
}

func scoutNativeActionFromArgs(toolID string, args map[string]any) scoutNativeAction {
	fields := make(map[string]string, len(args))
	for key, value := range args {
		switch typed := value.(type) {
		case string:
			fields[key] = typed
		case []string:
			fields[key] = strings.Join(typed, ", ")
		default:
			fields[key] = fmt.Sprint(value)
		}
	}
	return scoutNativeAction{ToolID: toolID, Fields: fields}
}

func exactVisibleChannel(app *kanbanBoardApp, requesterEmail string, name string) (scoutChatThreadRecord, error) {
	wanted := strings.TrimPrefix(strings.TrimSpace(name), "#")
	var match scoutChatThreadRecord
	matches := 0
	for _, thread := range app.scoutChatThreadsSnapshot(requesterEmail, true, 0) {
		if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || !strings.EqualFold(strings.TrimSpace(thread.Title), wanted) {
			continue
		}
		match = thread
		matches++
	}
	if matches != 1 {
		return scoutChatThreadRecord{}, fmt.Errorf("channel %q was not found", wanted)
	}
	return match, nil
}

func exactManagedFileFolder(name string, user *userAccount) (fileFolderRecord, error) {
	wanted := strings.TrimSpace(name)
	var match fileFolderRecord
	matches := 0
	for _, folder := range listFileFolders() {
		if !strings.EqualFold(strings.TrimSpace(folder.Name), wanted) || !fileFolderManagedByUser(folder.ID, user) {
			continue
		}
		match = folder
		matches++
	}
	if matches != 1 {
		return fileFolderRecord{}, fmt.Errorf("folder %q was not found", wanted)
	}
	return match, nil
}

func exactWritableFile(ctx context.Context, app *kanbanBoardApp, user *userAccount, name string) (assistantFileRecord, error) {
	wanted := strings.TrimSpace(name)
	var match assistantFileRecord
	matches := 0
	for _, row := range app.assistantFilesForPrincipal(ctx, user) {
		if strings.EqualFold(strings.TrimSpace(row.Name), wanted) && row.CanDelete {
			match = row
			matches++
		}
	}
	if matches != 1 {
		return assistantFileRecord{}, fmt.Errorf("file %q was not found or is ambiguous", wanted)
	}
	return match, nil
}

// executeScoutNativeAction reauthorizes every target by stable id immediately
// before mutation. The returned map is the execution receipt; callers may say
// "done" only after this function succeeds.
func (app *kanbanBoardApp) executeScoutNativeAction(ctx context.Context, user *userAccount, action scoutNativeAction) (map[string]any, bool, error) {
	if app == nil || user == nil {
		return nil, false, fmt.Errorf("Stride actions are unavailable")
	}
	args := scoutNativeActionArgs(action.Fields)
	switch action.ToolID {
	case "archive_channel":
		thread, err := exactVisibleChannel(app, user.Email, asString(args["channel"]))
		if err != nil {
			return nil, false, err
		}
		if thread.ArchivedAt != "" {
			return map[string]any{"ok": true, "action": action.ToolID, "threadId": thread.ID, "channel": thread.Title, "changed": false, "summary": "#" + thread.Title + " is already archived."}, false, nil
		}
		thread, err = app.setScoutChatThreadArchived(user.Email, thread.ID, true)
		if err != nil {
			return nil, false, err
		}
		deliverScoutChatThreadMetadata(thread)
		return map[string]any{"ok": true, "action": action.ToolID, "threadId": thread.ID, "channel": thread.Title, "changed": true, "summary": "Archived #" + thread.Title + "."}, false, nil
	case "rename_channel":
		thread, err := exactVisibleChannel(app, user.Email, asString(args["channel"]))
		if err != nil {
			return nil, false, err
		}
		thread, err = app.renameScoutChatThread(user.Email, thread.ID, asString(args["new_name"]))
		if err != nil {
			return nil, false, err
		}
		return map[string]any{"ok": true, "action": action.ToolID, "threadId": thread.ID, "channel": thread.Title, "changed": true, "summary": "Renamed the channel to #" + thread.Title + "."}, false, nil
	case "create_file_folder":
		folder, err := createFileFolder(asString(args["name"]), normalizeAccountEmail(user.Email))
		if err != nil {
			return nil, false, err
		}
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
		return map[string]any{"ok": true, "action": action.ToolID, "folderId": folder.ID, "folder": folder.Name, "changed": true, "summary": "Created the " + folder.Name + " folder."}, false, nil
	case "rename_file_folder":
		folder, err := exactManagedFileFolder(asString(args["folder"]), user)
		if err != nil {
			return nil, false, err
		}
		folder, err = sharedFileFolderStore().rename(folder.ID, asString(args["new_name"]))
		if err != nil {
			return nil, false, err
		}
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
		return map[string]any{"ok": true, "action": action.ToolID, "folderId": folder.ID, "folder": folder.Name, "changed": true, "summary": "Renamed the folder to " + folder.Name + "."}, false, nil
	case "delete_file_folder":
		folder, err := exactManagedFileFolder(asString(args["folder"]), user)
		if err != nil {
			return nil, false, err
		}
		if err := sharedFileFolderStore().remove(folder.ID); err != nil {
			return nil, false, err
		}
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
		return map[string]any{"ok": true, "action": action.ToolID, "folderId": folder.ID, "folder": folder.Name, "changed": true, "summary": "Deleted the " + folder.Name + " folder. Its files are back in All files."}, false, nil
	case "delete_file":
		row, err := exactWritableFile(ctx, app, user, asString(args["file"]))
		if err != nil {
			return nil, false, err
		}
		mode, err := app.deleteAssistantFileForUser(ctx, user, row.ID)
		if err != nil {
			return nil, false, err
		}
		return map[string]any{"ok": true, "action": action.ToolID, "fileId": row.ID, "file": row.Name, "mode": mode, "changed": true, "summary": "Removed " + row.Name + " from Drive."}, false, nil
	default:
		result, changed, err := app.applyPrivateRealtimeVoiceTool(user.Email, action.ToolID, args)
		if err != nil {
			return nil, false, err
		}
		if result == nil {
			result = map[string]any{"ok": true}
		}
		result["action"] = action.ToolID
		result["changed"] = changed
		if _, ok := result["summary"]; !ok {
			result["summary"] = scoutNativeActionSuccessSummary(action.ToolID, args, result)
		}
		return result, changed, nil
	}
}

func scoutNativeActionSuccessSummary(toolID string, args map[string]any, result map[string]any) string {
	switch toolID {
	case "control_app":
		return "Opened " + assistantToolLabel(asString(args["tool"])) + "."
	case "create_channel":
		return "Created #" + firstNonBlank(asString(result["channel"]), asString(args["name"])) + "."
	case "post_to_channel":
		return "Posted to #" + firstNonBlank(asString(result["channel"]), asString(args["channel"])) + "."
	case "create_ticket":
		return "Added " + asString(args["title"]) + " to the Board."
	case "move_ticket", "update_ticket":
		return "Updated the Board card."
	case "delete_ticket":
		return "Deleted the Board card."
	case "undo_delete_ticket":
		return "Restored the last deleted Board card."
	case "send_notification":
		return "Sent the notification."
	default:
		return "Done."
	}
}
