type SocketEvent = { code?: number; data?: string; reason?: string };
type SocketListener = (event: SocketEvent) => void;

export class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  readonly closes: Array<{ code?: number; reason?: string }> = [];
  private readonly listeners = new Map<string, SocketListener[]>();

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: SocketListener) {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  close(code?: number, reason?: string) {
    this.closes.push({ code, reason });
  }

  open() {
    this.emit('open', {});
  }

  message(value: unknown) {
    this.emit('message', { data: JSON.stringify(value) });
  }

  rawMessage(data: string) {
    this.emit('message', { data });
  }

  serverClose(code = 1006, reason = '') {
    this.emit('close', { code, reason });
  }

  static reset() {
    FakeWebSocket.instances = [];
  }

  private emit(type: string, event: SocketEvent) {
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}
