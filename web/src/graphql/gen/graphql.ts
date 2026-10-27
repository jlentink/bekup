// @ts-nocheck
/* eslint-disable */
/** Internal type. DO NOT USE DIRECTLY. */
type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
/** Internal type. DO NOT USE DIRECTLY. */
export type Incremental<T> = T | { [P in keyof T]?: P extends ' $fragmentName' | '__typename' ? T[P] : never };
import type { TypedDocumentNode as DocumentNode } from '@graphql-typed-document-node/core';
/** What happened to a session. */
export type SessionEventKind =
  /** All of your sessions ended (password, role or status change). */
  | 'ALL_REVOKED'
  /** The session ended (logout, revoked, replaced by a new login). */
  | 'REVOKED'
  /** The session was replaced by a new one (privilege change). */
  | 'ROTATED';

export type SessionsSubscriptionVariables = Exact<{ [key: string]: never; }>;


export type SessionsSubscription = { sessionEvents: { kind: SessionEventKind } };

export type ServerInfoQueryVariables = Exact<{ [key: string]: never; }>;


export type ServerInfoQuery = { serverInfo: { version: string, insecureTransport: boolean, devMode: boolean } };


export const SessionsDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"subscription","name":{"kind":"Name","value":"Sessions"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"sessionEvents"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"kind"}}]}}]}}]} as unknown as DocumentNode<SessionsSubscription, SessionsSubscriptionVariables>;
export const ServerInfoDocument = {"kind":"Document","definitions":[{"kind":"OperationDefinition","operation":"query","name":{"kind":"Name","value":"ServerInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"serverInfo"},"selectionSet":{"kind":"SelectionSet","selections":[{"kind":"Field","name":{"kind":"Name","value":"version"}},{"kind":"Field","name":{"kind":"Name","value":"insecureTransport"}},{"kind":"Field","name":{"kind":"Name","value":"devMode"}}]}}]}}]} as unknown as DocumentNode<ServerInfoQuery, ServerInfoQueryVariables>;