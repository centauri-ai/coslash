import type { MachineFact } from '@/pages/coslash/lib/machines';

export function sshTestFailureMessage(machine: MachineFact, sshAlias: string) {
  switch (machine.reason) {
    case 'refresh_timeout':
      return `The SSH host did not respond before the connection timed out. Check your network or VPN, run \`ssh ${sshAlias}\` in Terminal, then retry.`;
    case 'authentication_failed':
      return `SSH authentication was rejected. Run \`ssh ${sshAlias}\` in Terminal and fix the key, agent, or login prompt, then retry.`;
    case 'host_key_failed':
      return `The SSH host key is not trusted. Run \`ssh ${sshAlias}\` in Terminal, confirm the host key, then retry.`;
    case 'connection_failed':
      return `coSlash could not reach the SSH host. Check the alias, network or VPN, and SSH port; then run \`ssh ${sshAlias}\` in Terminal and retry.`;
    case 'sftp_unavailable':
      return `SSH connected, but the SFTP subsystem is unavailable. Run \`sftp ${sshAlias}\` in Terminal and enable SFTP on the host before retrying.`;
    case 'permission_denied':
      return `SSH connected, but the remote files are not readable. Check the account's file permissions, then retry.`;
    default:
      return `${machine.error ?? 'SSH verification failed.'} Run \`ssh ${sshAlias}\` in Terminal, resolve the reported problem, then retry.`;
  }
}
