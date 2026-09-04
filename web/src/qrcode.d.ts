declare module 'qrcode' {
  type QRCodeOptions = { width?: number; margin?: number; color?: { dark?: string; light?: string }; errorCorrectionLevel?: 'L' | 'M' | 'Q' | 'H' }
  const QRCode: { toCanvas(canvas: HTMLCanvasElement, text: string, options?: QRCodeOptions): Promise<void> }
  export default QRCode
}
