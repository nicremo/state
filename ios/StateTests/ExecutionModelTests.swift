import Foundation
import XCTest
@testable import State

final class ExecutionModelTests: XCTestCase {
    func testDecodesProjectListResponse() throws {
        let json = Data(
            """
            {
              "projects": [
                {
                  "id": "01989f00-0000-7000-8000-000000000050",
                  "name": "customer-api",
                  "description": "Customer API service",
                  "root_path_hint": "~/src/customer-api",
                  "revision": 2,
                  "created_at": "2026-08-11T08:00:00Z",
                  "updated_at": "2026-08-12T09:30:00Z"
                }
              ]
            }
            """.utf8
        )

        let response = try StateJSON.decoder.decode(ProjectListResponse.self, from: json)

        let project = try XCTUnwrap(response.projects.first)
        XCTAssertEqual(project.id, "01989f00-0000-7000-8000-000000000050")
        XCTAssertEqual(project.name, "customer-api")
        XCTAssertEqual(project.description, "Customer API service")
        XCTAssertEqual(project.rootPathHint, "~/src/customer-api")
        XCTAssertEqual(project.revision, 2)
    }

    func testDecodesPolicyListResponse() throws {
        let json = Data(
            """
            {
              "policies": [
                {
                  "id": "01989f00-0000-7000-8000-000000000051",
                  "name": "nightly-maintenance",
                  "project_id": "01989f00-0000-7000-8000-000000000050",
                  "adapter": "claude-code",
                  "mode": "unattended-low-risk",
                  "allowed_capabilities": ["read_repository", "run_tests"],
                  "mark_occurrence_done_on_success": true,
                  "notify_on_start": false,
                  "notify_on_completion": true,
                  "notify_on_failure": true,
                  "timeout_minutes": 30,
                  "enabled": true,
                  "revision": 3,
                  "created_at": "2026-08-11T08:00:00Z",
                  "updated_at": "2026-08-12T09:30:00Z"
                },
                {
                  "id": "01989f00-0000-7000-8000-000000000052",
                  "name": "empty-capabilities",
                  "project_id": "01989f00-0000-7000-8000-000000000050",
                  "adapter": "codex",
                  "mode": "supervised",
                  "allowed_capabilities": null,
                  "mark_occurrence_done_on_success": false,
                  "notify_on_start": false,
                  "notify_on_completion": false,
                  "notify_on_failure": false,
                  "timeout_minutes": 15,
                  "enabled": false,
                  "revision": 1,
                  "created_at": "2026-08-11T08:00:00Z",
                  "updated_at": "2026-08-11T08:00:00Z"
                }
              ]
            }
            """.utf8
        )

        let response = try StateJSON.decoder.decode(PolicyListResponse.self, from: json)

        XCTAssertEqual(response.policies.count, 2)
        let policy = try XCTUnwrap(response.policies.first)
        XCTAssertEqual(policy.projectID, "01989f00-0000-7000-8000-000000000050")
        XCTAssertEqual(policy.mode, .unattendedLowRisk)
        XCTAssertEqual(policy.allowedCapabilities, ["read_repository", "run_tests"])
        XCTAssertTrue(policy.markOccurrenceDoneOnSuccess)
        XCTAssertFalse(policy.notifyOnStart)
        XCTAssertTrue(policy.notifyOnCompletion)
        XCTAssertTrue(policy.notifyOnFailure)
        XCTAssertEqual(policy.timeoutMinutes, 30)
        XCTAssertTrue(policy.enabled)
        // The server nulls empty capability lists.
        XCTAssertEqual(response.policies[1].allowedCapabilities, [])
    }

    func testDecodesRunnerListResponse() throws {
        let json = Data(
            """
            {
              "runners": [
                {
                  "id": "01989f00-0000-7000-8000-000000000052",
                  "display_name": "Mac mini",
                  "projects": ["01989f00-0000-7000-8000-000000000050"],
                  "adapters": ["claude-code", "codex"],
                  "registered_at": "2026-08-11T08:00:00Z",
                  "last_seen_at": "2026-08-12T09:30:00Z",
                  "revision": 1
                },
                {
                  "id": "01989f00-0000-7000-8000-000000000053",
                  "display_name": "CI box",
                  "projects": null,
                  "adapters": null,
                  "registered_at": "2026-08-11T08:00:00Z",
                  "last_seen_at": "2026-08-11T08:00:00Z",
                  "revision": 2
                }
              ]
            }
            """.utf8
        )

        let response = try StateJSON.decoder.decode(RunnerListResponse.self, from: json)

        let runner = try XCTUnwrap(response.runners.first)
        XCTAssertEqual(runner.displayName, "Mac mini")
        XCTAssertEqual(runner.projects, ["01989f00-0000-7000-8000-000000000050"])
        XCTAssertEqual(runner.adapters, ["claude-code", "codex"])
        XCTAssertEqual(response.runners[1].projects, [])
        XCTAssertEqual(response.runners[1].adapters, [])
    }

    func testDecodesAgentRunWithNestedTaskContract() throws {
        let json = Data(
            """
            {
              "runs": [
                {
                  "id": "01989f00-0000-7000-8000-000000000060",
                  "reminder_id": "01989f00-0000-7000-8000-000000000010",
                  "occurrence_id": null,
                  "policy_id": "01989f00-0000-7000-8000-000000000051",
                  "policy_revision": 3,
                  "project_id": "01989f00-0000-7000-8000-000000000050",
                  "adapter": "claude-code",
                  "runner_id": "01989f00-0000-7000-8000-000000000052",
                  "status": "needs_approval",
                  "idempotency_key": "request-1",
                  "task_contract": {
                    "run_id": "01989f00-0000-7000-8000-000000000060",
                    "correlation_id": "01989f00-0000-7000-8000-000000000060",
                    "objective": "Fix the failing tests",
                    "project_id": "01989f00-0000-7000-8000-000000000050",
                    "project_name": "customer-api",
                    "policy_id": "01989f00-0000-7000-8000-000000000051",
                    "policy_revision": 3,
                    "contract_hash": "abc123",
                    "allowed_capabilities": ["read_repository", "run_tests"],
                    "timeout_minutes": 30,
                    "completion_rule": "mark_occurrence_done_on_success"
                  },
                  "context_cursor": 41,
                  "lease_expires_at": null,
                  "requested_at": "2026-08-12T09:00:00Z",
                  "claimed_at": "2026-08-12T09:01:00Z",
                  "started_at": "2026-08-12T09:01:30Z",
                  "finished_at": null,
                  "approval_capability": "deploy",
                  "created_by_actor": {
                    "id": "01989f00-0000-7000-8000-000000000001",
                    "kind": "owner",
                    "display_name": "Fabian",
                    "device_name": "iPhone"
                  },
                  "revision": 3,
                  "created_at": "2026-08-12T09:00:00Z",
                  "updated_at": "2026-08-12T09:02:00Z"
                }
              ]
            }
            """.utf8
        )

        let response = try StateJSON.decoder.decode(RunListResponse.self, from: json)

        let run = try XCTUnwrap(response.runs.first)
        XCTAssertEqual(run.reminderID, "01989f00-0000-7000-8000-000000000010")
        XCTAssertNil(run.occurrenceID)
        XCTAssertEqual(run.policyID, "01989f00-0000-7000-8000-000000000051")
        XCTAssertEqual(run.policyRevision, 3)
        XCTAssertEqual(run.projectID, "01989f00-0000-7000-8000-000000000050")
        XCTAssertEqual(run.runnerID, "01989f00-0000-7000-8000-000000000052")
        XCTAssertEqual(run.status, .needsApproval)
        XCTAssertEqual(run.idempotencyKey, "request-1")
        XCTAssertEqual(run.contextCursor, 41)
        XCTAssertNil(run.leaseExpiresAt)
        XCTAssertNotNil(run.requestedAt)
        XCTAssertNotNil(run.claimedAt)
        XCTAssertNil(run.finishedAt)
        XCTAssertNil(run.resultSummary)
        XCTAssertNil(run.failureCode)
        XCTAssertEqual(run.approvalCapability, "deploy")
        XCTAssertEqual(run.createdByActor?.kind, .owner)
        XCTAssertNil(run.completedByActor)
        let contract = run.taskContract
        XCTAssertEqual(contract.runID, run.id)
        XCTAssertEqual(contract.policyID, "01989f00-0000-7000-8000-000000000051")
        XCTAssertEqual(contract.projectName, "customer-api")
        XCTAssertEqual(contract.contractHash, "abc123")
        XCTAssertEqual(contract.allowedCapabilities, ["read_repository", "run_tests"])
        XCTAssertEqual(contract.acceptanceCriteria, [])
        XCTAssertEqual(contract.completionRule, "mark_occurrence_done_on_success")
    }

    func testDecodesReminderDetailWithPolicyAndRuns() throws {
        let json = Data(
            """
            {
              "reminder": {
                "id": "01989f00-0000-7000-8000-000000000010",
                "title": "Fix the failing tests",
                "status": "active",
                "execution_policy_id": "01989f00-0000-7000-8000-000000000051",
                "revision": 2,
                "archived": false,
                "created_at": "2026-08-11T08:00:00Z",
                "updated_at": "2026-08-12T09:30:00Z"
              },
              "comments": [],
              "occurrences": [],
              "history": [],
              "runs": []
            }
            """.utf8
        )

        let detail = try StateJSON.decoder.decode(ReminderDetail.self, from: json)

        XCTAssertEqual(detail.reminder.executionPolicyID, "01989f00-0000-7000-8000-000000000051")
        XCTAssertEqual(detail.runs, [])
    }

    func testDecodesChangeEventsWithoutReminder() throws {
        let json = Data(
            """
            {
              "changes": [
                {
                  "cursor": 11,
                  "event": {
                    "id": "01989f00-0000-7000-8000-000000000070",
                    "reminder_id": null,
                    "action": "policy.created",
                    "actor": {"id": "01989f00-0000-7000-8000-000000000001", "kind": "owner"},
                    "server_time": "2026-08-12T09:00:00Z",
                    "changed_fields": ["name"],
                    "revision": 1,
                    "correlation_id": "correlation",
                    "client_request_id": "request",
                    "hash": "hash",
                    "signature": "signature"
                  }
                },
                {
                  "cursor": 12,
                  "event": {
                    "id": "01989f00-0000-7000-8000-000000000071",
                    "reminder_id": "",
                    "action": "runner.registered",
                    "actor": {"id": "01989f00-0000-7000-8000-000000000052", "kind": "runner"},
                    "server_time": "2026-08-12T09:01:00Z",
                    "changed_fields": [],
                    "revision": 1,
                    "correlation_id": "correlation",
                    "client_request_id": "request",
                    "hash": "hash",
                    "signature": "signature"
                  }
                }
              ],
              "cursor": 12
            }
            """.utf8
        )

        let response = try StateJSON.decoder.decode(ChangesResponse.self, from: json)

        XCTAssertEqual(response.changes.count, 2)
        // The audit column stores NULL while the event JSON stores an empty
        // string; both mean "no reminder".
        XCTAssertNil(response.changes[0].event.reminderID)
        XCTAssertNil(response.changes[1].event.reminderID)
        XCTAssertEqual(response.changes[1].event.actor.kind, .runner)
    }

    func testAgentRunCacheRoundTrip() throws {
        let run = AgentRun(
            id: "01989f00-0000-7000-8000-000000000060",
            reminderID: "01989f00-0000-7000-8000-000000000010",
            occurrenceID: "01989f00-0000-7000-8000-000000000020",
            policyID: "01989f00-0000-7000-8000-000000000051",
            policyRevision: 3,
            projectID: "01989f00-0000-7000-8000-000000000050",
            adapter: "claude-code",
            runnerID: "01989f00-0000-7000-8000-000000000052",
            status: .succeeded,
            idempotencyKey: "request-1",
            taskContract: TaskContract(
                runID: "01989f00-0000-7000-8000-000000000060",
                correlationID: "01989f00-0000-7000-8000-000000000060",
                objective: "Fix the failing tests",
                acceptanceCriteria: ["Suite is green"],
                projectID: "01989f00-0000-7000-8000-000000000050",
                projectName: "customer-api",
                policyID: "01989f00-0000-7000-8000-000000000051",
                policyRevision: 3,
                contractHash: "abc123",
                allowedCapabilities: ["run_tests"],
                timeoutMinutes: 30,
                completionRule: "mark_occurrence_done_on_success"
            ),
            contextCursor: 41,
            leaseExpiresAt: nil,
            requestedAt: Date(timeIntervalSince1970: 1_786_000_000),
            claimedAt: Date(timeIntervalSince1970: 1_786_000_060),
            startedAt: Date(timeIntervalSince1970: 1_786_000_090),
            finishedAt: Date(timeIntervalSince1970: 1_786_000_300),
            resultSummary: "Suite is green.",
            resultArtifactRef: nil,
            failureCode: nil,
            approvalCapability: nil,
            createdByActor: nil,
            completedByActor: nil,
            revision: 4,
            createdAt: Date(timeIntervalSince1970: 1_786_000_000),
            updatedAt: Date(timeIntervalSince1970: 1_786_000_300)
        )

        let encoded = try StateJSON.encoder.encode(run)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: encoded) as? [String: Any])
        XCTAssertEqual(object["policy_id"] as? String, "01989f00-0000-7000-8000-000000000051")
        XCTAssertEqual(object["status"] as? String, "succeeded")
        XCTAssertNotNil(object["task_contract"])

        let decoded = try StateJSON.decoder.decode(AgentRun.self, from: encoded)
        XCTAssertEqual(decoded, run)
    }
}
