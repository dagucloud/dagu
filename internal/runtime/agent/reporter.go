// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/jedib0t/go-pretty/v6/table"
)

// formatCommands formats a slice of CommandEntry into a display string.
func formatCommands(commands []ir.CommandEntry) string {
	parts := make([]string, 0, len(commands))
	for _, cmd := range commands {
		if s := cmd.String(); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "; ")
}

// Sender is a mailer interface.
type Sender interface {
	Send(ctx context.Context, from string, to []string, subject, body string, attachments []string) error
}

// SenderFn is a function type for sending reports.
type SenderFn func(ctx context.Context, from string, to []string, subject, body string, attachments []string) error

// reporter is responsible for reporting the status of the runner
// to the user.
type reporter struct {
	senderFn  SenderFn
	errorMail *ir.MailConfig
	infoMail  *ir.MailConfig
	waitMail  *ir.MailConfig
}

// reporterConfig holds the evaluated mail configurations for the reporter.
type reporterConfig struct {
	ErrorMail *ir.MailConfig
	InfoMail  *ir.MailConfig
	WaitMail  *ir.MailConfig
}

func newReporter(f SenderFn, cfg reporterConfig) *reporter {
	return &reporter{
		senderFn:  f,
		errorMail: cfg.ErrorMail,
		infoMail:  cfg.InfoMail,
		waitMail:  cfg.WaitMail,
	}
}

// reportStep is a function that reports the status of a step.
func (r *reporter) reportStep(
	ctx context.Context, dag *ir.DAG, dagStatus dagrun.DAGRunStatus, node *runtime.Node,
) error {
	if r == nil {
		return nil
	}
	if r.errorMail == nil {
		return nil
	}
	if node.State().Status == ir.NodeFailed && node.NodeData().Step.MailOnError {
		return r.sendConfiguredMail(ctx, r.errorMail, dag.Name, dagStatus)
	}
	return nil
}

// send is a function that sends a report mail.
func (r *reporter) send(ctx context.Context, dag *ir.DAG, dagStatus dagrun.DAGRunStatus, err error) error {
	if r == nil {
		return nil
	}
	mailConfig := r.selectMailConfig(dag, dagStatus, err)
	if mailConfig == nil {
		return nil
	}
	return r.sendConfiguredMail(ctx, mailConfig, dag.Name, dagStatus)
}

// selectMailConfig returns the appropriate mail config based on status, or nil if no mail should be sent.
func (r *reporter) selectMailConfig(dag *ir.DAG, dagStatus dagrun.DAGRunStatus, err error) *ir.MailConfig {
	if dag.MailOn == nil {
		return nil
	}

	switch {
	case (err != nil || dagStatus.Status == ir.Failed) && dag.MailOn.Failure:
		return r.errorMail
	case (dagStatus.Status == ir.Succeeded || dagStatus.Status == ir.PartiallySucceeded) && dag.MailOn.Success:
		return r.infoMail
	case dagStatus.Status == ir.Waiting && dag.MailOn.Wait:
		return r.waitMail
	default:
		return nil
	}
}

func (r *reporter) sendConfiguredMail(ctx context.Context, mailConfig *ir.MailConfig, dagName string, dagStatus dagrun.DAGRunStatus) error {
	return r.senderFn(
		ctx,
		mailConfig.From,
		mailConfig.To,
		buildMailSubject(mailConfig.Prefix, dagName, dagStatus.Status),
		renderHTMLWithDAGInfo(dagStatus),
		addAttachments(mailConfig.AttachLogs, dagStatus.Nodes),
	)
}

func buildMailSubject(prefix, dagName string, status ir.Status) string {
	return fmt.Sprintf("%s %s (%s)", prefix, dagName, status)
}

func statusToClass(status string) string {
	switch status {
	case "finished", "succeeded":
		return "status-finished"
	case "failed":
		return "status-failed"
	case "running":
		return "status-running"
	case "skipped":
		return "status-skipped"
	case "partially_succeeded":
		return "status-partial-success"
	case "aborted":
		return "status-aborted"
	case "waiting":
		return "status-waiting"
	default:
		return ""
	}
}

func statusBadgeClass(status string) string {
	switch status {
	case "finished", "succeeded":
		return "success"
	case "partially_succeeded":
		return "success"
	case "failed":
		return "failed"
	case "running":
		return "running"
	case "skipped":
		return "skipped"
	case "aborted":
		return "aborted"
	case "waiting":
		return "wait"
	default:
		return ""
	}
}

var dagHeader = table.Row{
	"Run ID",
	"Name",
	"Started At",
	"Finished At",
	"Status",
	"Params",
	"Error",
}

func renderDAGSummary(dagStatus dagrun.DAGRunStatus, err error) string {
	errText := ""
	if err != nil {
		errText = err.Error()
	}

	dataRow := table.Row{
		dagStatus.DAGRunID,
		dagStatus.Name,
		dagStatus.StartedAt,
		dagStatus.FinishedAt,
		dagStatus.Status,
		dagStatus.Params,
		errText,
	}

	reportTable := table.NewWriter()
	reportTable.AppendHeader(dagHeader)
	reportTable.AppendRow(dataRow)
	return reportTable.Render()
}

var stepHeader = table.Row{
	"#",
	"Step",
	"Started At",
	"Finished At",
	"Status",
	"Command",
	"Error",
}

func renderStepSummary(nodes []*dagrun.Node) string {
	stepTable := table.NewWriter()
	stepTable.AppendHeader(stepHeader)

	for i, n := range nodes {
		stepTable.AppendRow(table.Row{
			fmt.Sprintf("%d", i+1),
			n.Step.Name,
			n.StartedAt,
			n.FinishedAt,
			n.Status.String(),
			formatCommands(n.Step.Commands),
			n.Error,
		})
	}

	return stepTable.Render()
}

func renderHTML(nodes []*dagrun.Node) string {
	var buffer bytes.Buffer

	// Start with basic HTML structure with improved styling
	_, _ = buffer.WriteString(`<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style type="text/css">
        body { margin: 0; padding: 20px; background-color: #fafbfc; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
        table { 
            border-collapse: separate; 
            border-spacing: 0; 
            width: 100%; 
            max-width: 1200px;
            margin: 0 auto;
            background-color: #ffffff; 
            border-radius: 12px; 
            overflow: hidden;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
        }
        th { 
            background-color: #2563eb;
            color: #ffffff; 
            font-weight: 600; 
            padding: 16px 12px; 
            text-align: left;
            font-size: 14px;
            letter-spacing: 0.5px;
            text-transform: uppercase;
        }
        td { 
            padding: 14px 12px; 
            border-bottom: 1px solid #e8eef3; 
            vertical-align: top;
            font-size: 13px;
            line-height: 1.5;
        }
        tr:hover { background-color: #f8f9fb; }
        tr:last-child td { border-bottom: none; }
        .status-finished { color: #059669; font-weight: 500; }
        .status-failed { color: #dc2626; font-weight: 500; }
        .status-running { color: #2563eb; font-weight: 500; }
        .status-skipped { color: #6b7280; font-weight: 500; }
        .status-aborted { color: #db2777; font-weight: 500; }
        .status-partial-success { color: #ea580c; font-weight: 500; }
        .status-waiting { color: #92400e; font-weight: 500; }
        .row-number { 
            background-color: #f1f5f9; 
            font-weight: 600; 
            text-align: center; 
            color: #475569;
            min-width: 40px;
        }
        .step-name { font-weight: 500; color: #1e293b; }
        .command-cell { font-family: "SF Mono", Monaco, "Cascadia Code", monospace; font-size: 12px; color: #475569; }
        .error-cell { color: #dc2626; font-size: 12px; }
        .timestamp { color: #64748b; font-size: 12px; }
    </style>
</head>
<body>
`)
	writeHTMLTable(&buffer, nodes)
	_, _ = buffer.WriteString("</body></html>")

	return buffer.String()
}

var htmlTableHeaders = []string{"#", "Step", "Started At", "Finished At", "Status", "Command", "Error"}

func writeHTMLTable(buffer *bytes.Buffer, nodes []*dagrun.Node) {
	_, _ = buffer.WriteString("<table><thead><tr>")
	writeHTMLHeaderCells(buffer, htmlTableHeaders)
	_, _ = buffer.WriteString("</tr></thead><tbody>")
	for i, n := range nodes {
		writeNodeRow(buffer, i, n)
	}
	_, _ = buffer.WriteString("</tbody></table>")
}

func writeHTMLHeaderCells(buffer *bytes.Buffer, headers []string) {
	for _, header := range headers {
		_, _ = buffer.WriteString("<th>")
		_, _ = buffer.WriteString(header)
		_, _ = buffer.WriteString("</th>")
	}
}

// writeNodeRow writes a single node row to the HTML buffer.
func writeNodeRow(buffer *bytes.Buffer, index int, n *dagrun.Node) {
	_, _ = buffer.WriteString("<tr>")
	_, _ = buffer.WriteString(fmt.Sprintf("<td class=\"row-number\">%d</td>", index+1))
	_, _ = buffer.WriteString(fmt.Sprintf("<td class=\"step-name\">%s</td>", html.EscapeString(n.Step.Name)))
	_, _ = buffer.WriteString(fmt.Sprintf("<td class=\"timestamp\">%s</td>", html.EscapeString(n.StartedAt)))
	_, _ = buffer.WriteString(fmt.Sprintf("<td class=\"timestamp\">%s</td>", html.EscapeString(n.FinishedAt)))

	status := n.Status.String()
	_, _ = buffer.WriteString(fmt.Sprintf("<td class=\"%s\">%s</td>", statusToClass(status), status))
	_, _ = buffer.WriteString(fmt.Sprintf("<td class=\"command-cell\">%s</td>", html.EscapeString(formatCommands(n.Step.Commands))))

	if n.Error != "" {
		_, _ = buffer.WriteString(fmt.Sprintf("<td class=\"error-cell\">%s</td>", html.EscapeString(n.Error)))
	} else {
		_, _ = buffer.WriteString("<td></td>")
	}
	_, _ = buffer.WriteString("</tr>")
}

func renderHTMLWithDAGInfo(dagStatus dagrun.DAGRunStatus) string {
	var buffer bytes.Buffer

	// Start with enhanced HTML structure and styling
	_, _ = buffer.WriteString(`<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style type="text/css">
        body { 
            margin: 0; 
            padding: 20px; 
            background-color: #f8fafc; 
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
            line-height: 1.6;
            color: #334155;
        }
        .container { 
            max-width: 1000px; 
            margin: 0 auto; 
        }
        .dag-info {
            background: linear-gradient(135deg, #f8fafc 0%, #ffffff 100%);
            border-radius: 16px;
            padding: 32px;
            margin-bottom: 32px;
            box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
            border: 1px solid #e2e8f0;
            position: relative;
            overflow: hidden;
        }
        .dag-info::before {
            content: "";
            position: absolute;
            top: 0;
            left: 0;
            right: 0;
            height: 4px;
            background: linear-gradient(90deg, #3b82f6 0%, #2563eb 50%, #1d4ed8 100%);
        }
        .dag-info h2 {
            margin: 32px 0 24px 0;
            color: #1e293b;
            font-size: 24px;
            font-weight: 700;
            letter-spacing: -0.5px;
        }
        .status-badge {
            position: absolute;
            top: 24px;
            right: 24px;
            display: inline-flex;
            align-items: center;
            padding: 8px 16px;
            border-radius: 24px;
            font-size: 13px;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.5px;
        }
        .status-badge.success {
            background-color: #d1fae5;
            color: #065f46;
            box-shadow: 0 0 0 1px rgba(16, 185, 129, 0.1);
        }
        .status-badge.failed {
            background-color: #fee2e2;
            color: #991b1b;
            box-shadow: 0 0 0 1px rgba(239, 68, 68, 0.1);
        }
        .status-badge.running {
            background-color: #dbeafe;
            color: #1e40af;
            box-shadow: 0 0 0 1px rgba(59, 130, 246, 0.1);
        }
        .status-badge.aborted {
            background-color: #fce7f3;
            color: #9d174d;
            box-shadow: 0 0 0 1px rgba(219, 39, 119, 0.15);
        }
        .status-badge.skipped {
            background-color: #f3f4f6;
            color: #374151;
            box-shadow: 0 0 0 1px rgba(107, 114, 128, 0.1);
        }
        .status-badge.wait {
            background-color: #fef3c7;
            color: #92400e;
            box-shadow: 0 0 0 1px rgba(245, 158, 11, 0.1);
        }
        .dag-info-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
            gap: 24px;
        }
        .info-item {
            background-color: #ffffff;
            padding: 16px;
            border-radius: 8px;
            border: 1px solid #e5e7eb;
        }
        .dag-info-label {
            font-weight: 600;
            color: #6b7280;
            font-size: 12px;
            text-transform: uppercase;
            letter-spacing: 0.5px;
            margin-bottom: 4px;
        }
        .dag-info-value {
            color: #1e293b;
            font-size: 16px;
            font-weight: 500;
        }
        .dag-info-value.mono {
            font-family: "SF Mono", "Consolas", "Monaco", monospace;
            font-size: 14px;
            color: #0f172a;
            background-color: #f1f5f9;
            padding: 6px 10px;
            border-radius: 4px;
            display: inline-block;
            margin-top: 4px;
            word-break: break-all;
        }
        .dag-name {
            color: #1e293b;
            font-size: 18px;
            font-weight: 600;
            margin-bottom: 4px;
        }
        table { 
            border-collapse: separate; 
            border-spacing: 0; 
            width: 100%; 
            background-color: #ffffff; 
            border-radius: 12px; 
            overflow: hidden;
            box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
            border: 1px solid #e2e8f0;
        }
        th { 
            background-color: #2563eb;
            color: #ffffff; 
            font-weight: 600; 
            padding: 16px 12px; 
            text-align: left;
            font-size: 14px;
            letter-spacing: 0.5px;
            text-transform: uppercase;
        }
        td { 
            padding: 14px 12px; 
            border-bottom: 1px solid #f1f5f9; 
            vertical-align: top;
            font-size: 13px;
            line-height: 1.5;
        }
        tr:hover { background-color: #f8fafc; }
        tr:last-child td { border-bottom: none; }
        .status-finished { color: #059669; font-weight: 500; }
        .status-failed { color: #dc2626; font-weight: 500; }
        .status-running { color: #2563eb; font-weight: 500; }
        .status-skipped { color: #6b7280; font-weight: 500; }
        .status-aborted { color: #db2777; font-weight: 500; }
        .status-partial-success { color: #ea580c; font-weight: 500; }
        .status-waiting { color: #92400e; font-weight: 500; }
        .row-number {
            background-color: #f1f5f9; 
            font-weight: 600; 
            text-align: center; 
            color: #475569;
            min-width: 40px;
        }
        .step-name { font-weight: 500; color: #1e293b; }
        .command-cell { font-family: "SF Mono", "Consolas", "Monaco", monospace; font-size: 12px; color: #475569; }
        .error-cell { color: #dc2626; font-size: 12px; }
        .timestamp { color: #64748b; font-size: 12px; }
    </style>
</head>
<body>
<div class="container">
    <div class="dag-info">
        <div class="status-badge `)

	statusStr := dagStatus.Status.String()
	_, _ = buffer.WriteString(statusBadgeClass(statusStr))
	_, _ = buffer.WriteString(`">`)
	_, _ = buffer.WriteString(strings.ToUpper(statusStr))
	_, _ = buffer.WriteString(`</div>
        <h2>DAG Execution Details</h2>
        <div class="dag-name">`)

	// Add DAG name (escaped)
	_, _ = buffer.WriteString(html.EscapeString(dagStatus.Name))

	_, _ = buffer.WriteString(`</div>
        <div class="dag-info-grid">
            <div class="info-item">
                <div class="dag-info-label">DAG Run ID</div>
                <div class="dag-info-value mono">`)

	// Add DAG Run ID (escaped)
	_, _ = buffer.WriteString(html.EscapeString(dagStatus.DAGRunID))

	_, _ = buffer.WriteString(`</div>
            </div>
            <div class="info-item">
                <div class="dag-info-label">Parameters</div>
                <div class="dag-info-value mono">`)

	// Add Parameters (escaped)
	params := dagStatus.Params
	if params == "" {
		params = "(none)"
	}
	params = html.EscapeString(params)
	params = strings.ReplaceAll(params, "\"", "&quot;")
	_, _ = buffer.WriteString(params)

	_, _ = buffer.WriteString(`</div>
            </div>
            <div class="info-item">
                <div class="dag-info-label">Started At</div>
                <div class="dag-info-value">`)
	_, _ = buffer.WriteString(dagStatus.StartedAt)

	_, _ = buffer.WriteString(`</div>
            </div>
            <div class="info-item">
                <div class="dag-info-label">Finished At</div>
                <div class="dag-info-value">`)
	_, _ = buffer.WriteString(dagStatus.FinishedAt)

	_, _ = buffer.WriteString(`</div>
            </div>
        </div>
    </div>
`)
	writeHTMLTable(&buffer, dagStatus.Nodes)
	_, _ = buffer.WriteString("</div></body></html>")

	return buffer.String()
}

func addAttachments(
	trigger bool, nodes []*dagrun.Node,
) (attachments []string) {
	if trigger {
		for _, n := range nodes {
			attachments = append(attachments, n.Stdout)
		}
	}
	return attachments
}
