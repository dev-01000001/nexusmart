declare namespace NodeJS {
  interface ProcessEnv {
    [key: string]: string | undefined;
  }
  interface Process {
    env: ProcessEnv;
  }
}

declare const process: NodeJS.Process;

declare module "node:fs" {
  export function existsSync(path: string): boolean;
  const fs: { existsSync: typeof existsSync };
  export default fs;
}
