#!/usr/bin/env python3
"""
Banana Pi Console Interface
A robust Python script for reading, executing commands, and monitoring the console

Enhanced with:
- Console wake-up mechanism
- Buffer clearing before commands
- Command verification and retry
- Improved timeout handling
- Better output parsing
"""

import serial
import time
import sys
import threading
from queue import Queue
from datetime import datetime
from pathlib import Path


class BPiConsole:
    """Robust interface for Banana Pi serial console"""

    # Default settings
    DEFAULT_PORT = '/dev/ttyUSB0'
    DEFAULT_BAUDRATE = 115200
    DEFAULT_TIMEOUT = 3
    WAKE_ATTEMPTS = 5
    MAX_RETRIES = 3

    def __init__(self, port=None, baudrate=None, timeout=None, log_file=None):
        """
        Initialize console connection

        Args:
            port: Serial port (default: /dev/ttyACM0)
            baudrate: Serial speed (default: 115200)
            timeout: Read timeout in seconds (default: 3)
            log_file: Optional log file path for all interactions
        """
        self.port = port or self.DEFAULT_PORT
        self.baudrate = baudrate or self.DEFAULT_BAUDRATE
        self.timeout = timeout or self.DEFAULT_TIMEOUT
        self.ser = None
        self.log_file = log_file
        self.output_queue = Queue()
        self.monitoring = False
        self.reader_thread = None
        self._console_ready = False

    def log(self, message, level="INFO"):
        """Log message to file and stdout"""
        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        log_msg = f"[{timestamp}] [{level}] {message}"
        print(log_msg)
        if self.log_file:
            try:
                with open(self.log_file, 'a') as f:
                    f.write(log_msg + '\n')
            except Exception:
                pass

    def connect(self):
        """Establish serial connection and wake console"""
        try:
            self.ser = serial.Serial(
                port=self.port,
                baudrate=self.baudrate,
                timeout=self.timeout
            )
            self.log(f"Connected to {self.port} at {self.baudrate} baud", "SUCCESS")

            # Initialize console
            if self._wake_console():
                self._console_ready = True
                return True
            else:
                self.log("Console connected but may not be fully responsive", "WARNING")
                return True

        except PermissionError:
            self.log(f"Permission denied on {self.port}. Add user to dialout group.", "ERROR")
            return False
        except serial.SerialException as e:
            self.log(f"Serial connection error: {e}", "ERROR")
            return False

    def _wake_console(self):
        """
        Wake up the console by sending newlines and checking for response

        Returns:
            True if console is responsive, False otherwise
        """
        if not self.ser or not self.ser.is_open:
            return False

        # Initial delay for connection to stabilize
        time.sleep(0.5)

        # Clear any pending data in buffer
        self._clear_buffer()

        # Send multiple newlines to wake console
        for attempt in range(self.WAKE_ATTEMPTS):
            self.ser.write(b'\n')
            time.sleep(0.2)

        # Wait for console to respond
        time.sleep(0.5)

        # Check if we got a prompt
        if self.ser.in_waiting:
            data = self.ser.read(self.ser.in_waiting)
            response = data.decode('utf-8', errors='ignore')
            if 'root@' in response or '#' in response or '~' in response:
                return True

        # Try one more time with echo test
        self.ser.write(b'echo WAKE_TEST\n')
        time.sleep(1)

        if self.ser.in_waiting:
            data = self.ser.read(self.ser.in_waiting)
            response = data.decode('utf-8', errors='ignore')
            if 'WAKE_TEST' in response:
                return True

        return False

    def _clear_buffer(self):
        """Clear receive buffer"""
        if self.ser and self.ser.is_open and self.ser.in_waiting:
            self.ser.read(self.ser.in_waiting)

    def disconnect(self):
        """Close serial connection"""
        if self.ser and self.ser.is_open:
            self.stop_monitor()
            self.ser.close()
            self._console_ready = False
            self.log("Disconnected", "INFO")

    def _reader_loop(self):
        """Background thread for continuous reading"""
        while self.monitoring and self.ser and self.ser.is_open:
            try:
                if self.ser.in_waiting:
                    data = self.ser.read(self.ser.in_waiting)
                    text = data.decode('utf-8', errors='ignore')
                    self.output_queue.put(text)
                    sys.stdout.write(text)
                    sys.stdout.flush()
                else:
                    time.sleep(0.05)
            except Exception as e:
                if self.monitoring:
                    self.log(f"Reader thread error: {e}", "ERROR")
                break

    def start_monitor(self):
        """Start continuous console monitoring"""
        if not self.ser or not self.ser.is_open:
            self.log("Not connected. Call connect() first.", "ERROR")
            return False

        if self.monitoring:
            self.log("Already monitoring", "WARNING")
            return False

        self.monitoring = True
        self.reader_thread = threading.Thread(target=self._reader_loop, daemon=True)
        self.reader_thread.start()
        self.log("Monitor started. Type commands (Ctrl+C to exit).", "INFO")
        return True

    def stop_monitor(self):
        """Stop continuous monitoring"""
        if self.monitoring:
            self.monitoring = False
            if self.reader_thread:
                self.reader_thread.join(timeout=2)
            self.log("Monitor stopped", "INFO")

    def write(self, data):
        """Write raw data to serial port"""
        if not self.ser or not self.ser.is_open:
            self.log("Not connected", "ERROR")
            return False
        try:
            self.ser.write(data)
            return True
        except Exception as e:
            self.log(f"Write error: {e}", "ERROR")
            return False

    def read(self, timeout=None, wait_for_data=True):
        """
        Read available data from serial port

        Args:
            timeout: Override default timeout (seconds)
            wait_for_data: If True, wait until data arrives or timeout

        Returns:
            String of output or None on timeout
        """
        if not self.ser or not self.ser.is_open:
            self.log("Not connected", "ERROR")
            return None

        read_timeout = timeout or self.timeout

        try:
            start_time = time.time()
            output = b''
            no_data_count = 0

            while True:
                if self.ser.in_waiting:
                    chunk = self.ser.read(self.ser.in_waiting)
                    output += chunk
                    no_data_count = 0
                    time.sleep(0.05)  # Small delay to allow more data to arrive
                else:
                    no_data_count += 1
                    # If we have data and no new data for a while, return
                    if output and no_data_count > 5:
                        break
                    # Check timeout
                    if (time.time() - start_time) > read_timeout:
                        break
                    time.sleep(0.1)

            return output.decode('utf-8', errors='ignore') if output else None

        except Exception as e:
            self.log(f"Read error: {e}", "ERROR")
            return None

    def execute(self, command, wait_time=1.5, read_timeout=3, retries=None):
        """
        Execute a command and return output

        Args:
            command: Command to execute (string)
            wait_time: Time to wait after sending command (seconds)
            read_timeout: Timeout for reading response (seconds)
            retries: Number of retries if no output (default: MAX_RETRIES)

        Returns:
            Tuple of (success, output)
        """
        if not self.ser or not self.ser.is_open:
            self.log("Not connected", "ERROR")
            return False, None

        max_retries = retries if retries is not None else self.MAX_RETRIES

        for attempt in range(max_retries):
            try:
                # Wake console if needed
                if attempt > 0:
                    self._wake_console()

                # Clear buffer before sending command
                self._clear_buffer()

                # Send newline first to ensure clean prompt
                self.ser.write(b'\n')
                time.sleep(0.2)
                self._clear_buffer()

                # Send command
                cmd_bytes = (command + '\n').encode('utf-8')
                self.ser.write(cmd_bytes)

                if attempt == 0:
                    self.log(f"Executed: {command}", "CMD")

                # Wait for execution
                time.sleep(wait_time)

                # Read output with extended timeout
                output = self.read(timeout=read_timeout)

                if output:
                    # Verify command was executed (should see command echo)
                    if command in output or 'root@' in output:
                        if attempt == 0:
                            self.log(f"Output:\n{output}", "OUTPUT")
                        return True, output
                    else:
                        # Got some output but might be stale
                        if attempt < max_retries - 1:
                            continue

                # No output - retry if attempts remaining
                if attempt < max_retries - 1:
                    self.log(f"No output, retrying ({attempt + 2}/{max_retries})...", "RETRY")
                    time.sleep(0.5)
                    continue

                return True, output

            except Exception as e:
                self.log(f"Execute error: {e}", "ERROR")
                if attempt < max_retries - 1:
                    time.sleep(0.5)
                    continue
                return False, None

        return False, None

    def execute_and_parse(self, command, wait_time=1.5, read_timeout=3):
        """
        Execute command and return parsed output (without command echo and prompts)

        Args:
            command: Command to execute
            wait_time: Time to wait after sending command
            read_timeout: Timeout for reading response

        Returns:
            Tuple of (success, parsed_output_lines)
        """
        success, output = self.execute(command, wait_time, read_timeout)

        if not success or not output:
            return success, []

        # Parse output - remove command echo and prompts
        lines = output.split('\n')
        parsed = []
        for line in lines:
            line = line.strip()
            # Skip empty lines, command echoes, and prompts
            if not line:
                continue
            if command in line:
                continue
            if line.startswith('root@'):
                continue
            if line == '#' or line == '~#':
                continue
            parsed.append(line)

        return success, parsed

    def interactive(self):
        """Interactive console mode"""
        if not self.ser or not self.ser.is_open:
            self.log("Not connected", "ERROR")
            return

        self.log("Entering interactive mode. Type 'exit' to quit.", "INFO")

        # Wake console first
        self._wake_console()

        self.start_monitor()

        try:
            while True:
                try:
                    cmd = input()
                    if cmd.lower() == 'exit':
                        break
                    if cmd:
                        self.write((cmd + '\n').encode('utf-8'))
                except EOFError:
                    break
        except KeyboardInterrupt:
            self.log("\nInterrupted", "INFO")
        except Exception as e:
            self.log(f"Interactive mode error: {e}", "ERROR")
        finally:
            self.stop_monitor()

    def check_responsive(self):
        """
        Check if console is responsive

        Returns:
            True if console responds to echo test
        """
        if not self.ser or not self.ser.is_open:
            return False

        self._clear_buffer()

        test_string = f"TEST_{int(time.time())}"
        self.ser.write(f'echo "{test_string}"\n'.encode())
        time.sleep(1)

        output = self.read(timeout=2)
        return output is not None and test_string in output


def main():
    """Command line interface"""
    import argparse

    parser = argparse.ArgumentParser(
        description='Banana Pi Console Interface - Robust serial console tool',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  %(prog)s --cmd "uname -a"           Execute single command
  %(prog)s --cmd "ip addr" --log out.log   Execute with logging
  %(prog)s --interactive              Interactive mode
  %(prog)s --monitor                  Monitor console output
  %(prog)s --test                     Test console responsiveness
        """
    )
    parser.add_argument('--port', default='/dev/ttyACM0', help='Serial port')
    parser.add_argument('--baudrate', type=int, default=115200, help='Baud rate')
    parser.add_argument('--timeout', type=int, default=3, help='Read timeout (seconds)')
    parser.add_argument('--log', help='Log file path')
    parser.add_argument('--cmd', help='Execute single command and exit')
    parser.add_argument('--monitor', action='store_true', help='Monitor console (Ctrl+C to exit)')
    parser.add_argument('--interactive', action='store_true', help='Interactive mode')
    parser.add_argument('--test', action='store_true', help='Test console responsiveness')
    parser.add_argument('--wait', type=float, default=1.5, help='Wait time after command (seconds)')
    parser.add_argument('--retries', type=int, default=3, help='Number of retries for commands')

    args = parser.parse_args()

    # Create console instance
    console = BPiConsole(
        port=args.port,
        baudrate=args.baudrate,
        timeout=args.timeout,
        log_file=args.log
    )

    # Connect
    if not console.connect():
        sys.exit(1)

    try:
        if args.test:
            # Test mode
            if console.check_responsive():
                console.log("Console is responsive", "SUCCESS")
                sys.exit(0)
            else:
                console.log("Console is not responding", "ERROR")
                sys.exit(1)

        elif args.cmd:
            # Execute single command
            success, output = console.execute(
                args.cmd,
                wait_time=args.wait,
                retries=args.retries
            )
            sys.exit(0 if success else 1)

        elif args.monitor:
            # Monitor mode
            console.start_monitor()
            try:
                while True:
                    time.sleep(1)
            except KeyboardInterrupt:
                console.log("\nMonitoring stopped", "INFO")

        else:
            # Default: interactive mode
            console.interactive()

    except Exception as e:
        console.log(f"Unexpected error: {e}", "ERROR")
        sys.exit(1)
    finally:
        console.disconnect()


if __name__ == '__main__':
    main()
