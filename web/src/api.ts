export class APIError extends Error {constructor(public status:number, message:string, public reason?:string){super(message)}}
export async function api<T>(route:string,token:string,body?:unknown):Promise<T>{
 const r=await fetch('/api/v1/'+route,{method:body===undefined?'GET':'POST',headers:{...(token?{Authorization:'Bearer '+token}:{}),...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body)})
 const v=await r.json();if(!r.ok||v.code!==0)throw new APIError(r.status,typeof v.data?.message==='string'?v.data.message:(v.msg||'请求失败'),v.data?.reason);return v.data as T
}
export interface Project{id:string;name:string;role:string;quota:number;createAt:string}
export interface Sandbox{workspaceId?:string;id:string;projectId:string;ownerId:string;name:string;image:string;backendId:string;state:string;ttl:number;createAt:string;expiresAt?:string}
export interface Audit{workspaceId?:string;conversationId?:string;id:number;sandboxId:string;action:string;actor:string;outcome:string;createAt:string}
export interface Member{userId:string;role:string}
export interface LogicalNamespace{id:string;ownerId:string;name:string}
export interface PersistentWorkspace{id:string;namespaceId:string;ownerId:string;projectId:string;name:string;state:string;pvcName:string;pvcUid:string;pvName:string;storageClass:string;requestedBytes:number;storagePhase:string;message:string;activeSandboxId:string;archivedAt?:string;lastObservedAt?:string}
export interface Conversation{id:string;namespaceId:string;projectId:string;ownerId:string;integrationId:string;externalId:string;title:string;workspaceId:string;version:number}

export interface RunContext{id:string;projectId:string;ownerId:string;actor:string;conversationId:string;workspaceId:string;bindingVersion:number;sandboxId:string;attachmentGeneration:number;image:string;ttl:number;state:string;reason:string;message:string;releasedAt:string|null;createAt:string;operation:{state:string;reason:string;message:string;attempt:number;nextAttemptAt:string}}
