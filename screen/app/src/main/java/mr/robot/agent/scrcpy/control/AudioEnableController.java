package mr.robot.agent.scrcpy.control;

/**
 * 由控制命令 SET_AUDIO_ENABLED 动态启停音频流（不重启进程）。
 */
public interface AudioEnableController {
    void setAudioEnabled(boolean enabled);
}
